package store

// Daily usage log — answers "is any AI agent really using Vectorizer?"
//
// Append-only JSONL: one line per API call {ts, action, source, workspace}.
// Aggregated in-memory and served by GET /api/v1/usage/daily.
//
// Storage: $USAGE_LOG_PATH, else /data/ai/usage/usage.jsonl when that dir
// is writable (persistent docker mount on ns539881), else ./data/usage.jsonl.
// If nothing is writable the tracker keeps counting in memory and warns once.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Usage actions. Keep the set small — Total is the headline signal.
const (
	UsageSearch = "search" // semantic/code/conclusion retrieval
	UsageStore  = "store"  // message/batch writes
	UsageAsk    = "ask"    // brain ask
	UsageChat   = "chat"   // peer chat
	UsageCode   = "code"   // code index/symbols/callers
	UsageUpload = "upload" // file ingest
	UsageOther  = "other"  // any other mutating call
)

// Usage sources — who made the call. The MCP bridge and the dashboard
// proxy identify themselves via the X-Source header; anything else is "api".
const (
	SourceMCP       = "mcp"
	SourceDashboard = "dashboard"
	SourceAPI       = "api"
)

// sanitizeAgent keeps the X-Agent caller label safe for JSON keys and the
// dashboard: lowercase alphanumerics plus - and _, max 32 chars.
// Empty input falls back to the source so "Daily Usage By Agent" degrades
// gracefully for old log lines and clients that don't send X-Agent yet.
func sanitizeAgent(agent, source string) string {
	a := strings.ToLower(strings.TrimSpace(agent))
	var b strings.Builder
	for _, r := range a {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 32 {
		s = s[:32]
	}
	if s == "" {
		if source != "" {
			return source
		}
		return SourceAPI
	}
	return s
}

// UsageEvent is one logged API call.
type UsageEvent struct {
	TS        time.Time `json:"ts"`
	Action    string    `json:"action"`
	Source    string    `json:"source"`
	Agent     string    `json:"agent,omitempty"`
	Workspace string    `json:"workspace,omitempty"`
}

// DayUsage is the per-day aggregate served to the dashboard.
type DayUsage struct {
	Date        string         `json:"date"` // YYYY-MM-DD (Asia/Kuala_Lumpur)
	Searches    int            `json:"searches"`
	Stores      int            `json:"stores"`
	Ask         int            `json:"ask"`
	Chat        int            `json:"chat"`
	Code        int            `json:"code"`
	Upload      int            `json:"upload"`
	Other       int            `json:"other"`
	Total       int            `json:"total"`
	BySource    map[string]int `json:"by_source"`
	ByAgent     map[string]int `json:"by_agent,omitempty"`
	ByWorkspace map[string]int `json:"by_workspace,omitempty"`
}

func (d *DayUsage) add(action, source, agent, workspace string) {
	switch action {
	case UsageSearch:
		d.Searches++
	case UsageStore:
		d.Stores++
	case UsageAsk:
		d.Ask++
	case UsageChat:
		d.Chat++
	case UsageCode:
		d.Code++
	case UsageUpload:
		d.Upload++
	default:
		d.Other++
	}
	d.Total++
	if d.BySource == nil {
		d.BySource = map[string]int{}
	}
	d.BySource[source]++
	if d.ByAgent == nil {
		d.ByAgent = map[string]int{}
	}
	d.ByAgent[sanitizeAgent(agent, source)]++
	if workspace != "" {
		if d.ByWorkspace == nil {
			d.ByWorkspace = map[string]int{}
		}
		d.ByWorkspace[workspace]++
	}
}

// usageRetentionDays caps how much history we keep on disk.
const usageRetentionDays = 90

// UsageTracker counts API usage per day, persisted as JSONL.
type UsageTracker struct {
	mu         sync.Mutex
	path       string
	days       map[string]*DayUsage
	file       *os.File
	persistent bool
	loc        *time.Location
}

// GlobalUsage is the process-wide tracker, wired in main.go.
var GlobalUsage = NewUsageTracker()

func usageLocation() *time.Location {
	if loc, err := time.LoadLocation("Asia/Kuala_Lumpur"); err == nil {
		return loc
	}
	return time.FixedZone("MYT", 8*3600)
}

func usageResolvePath() (string, bool) {
	if v := strings.TrimSpace(os.Getenv("USAGE_LOG_PATH")); v != "" {
		if err := os.MkdirAll(filepath.Dir(v), 0o755); err == nil {
			return v, true
		}
	}
	candidates := []string{
		filepath.Join("/data/ai/usage", "usage.jsonl"), // persistent docker mount
		filepath.Join("data", "usage.jsonl"),           // local dev
	}
	for _, p := range candidates {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			continue
		}
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			continue
		}
		f.Close()
		return p, true
	}
	return "", false
}

// NewUsageTracker loads recent history and opens the log for appends.
func NewUsageTracker() *UsageTracker {
	u := &UsageTracker{
		days: map[string]*DayUsage{},
		loc:  usageLocation(),
	}
	path, ok := usageResolvePath()
	if !ok {
		return u // in-memory only
	}
	u.path = path
	u.replayAndTrim()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return u
	}
	u.file = f
	u.persistent = true
	go u.syncLoop()
	return u
}

func (u *UsageTracker) dayKey(t time.Time) string {
	return t.In(u.loc).Format("2006-01-02")
}

// replayAndTrim loads history, dropping lines older than retention.
func (u *UsageTracker) replayAndTrim() {
	f, err := os.Open(u.path)
	if err != nil {
		return
	}
	defer f.Close()
	cutoff := time.Now().In(u.loc).AddDate(0, 0, -usageRetentionDays).Format("2006-01-02")
	var kept []string
	trimmed := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		var ev UsageEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.TS.IsZero() {
			continue
		}
		day := u.dayKey(ev.TS)
		if day < cutoff {
			trimmed = true
			continue
		}
		kept = append(kept, line)
		d := u.dayFor(day)
		d.add(ev.Action, ev.Source, ev.Agent, ev.Workspace)
	}
	if trimmed {
		_ = os.WriteFile(u.path, []byte(strings.Join(kept, "\n")+(func() string {
			if len(kept) > 0 {
				return "\n"
			}
			return ""
		})()), 0o644)
	}
}

func (u *UsageTracker) dayFor(day string) *DayUsage {
	d, ok := u.days[day]
	if !ok {
		d = &DayUsage{Date: day}
		u.days[day] = d
	}
	return d
}

// Record logs one API call. Never blocks the request on disk errors.
func (u *UsageTracker) Record(action, source, agent, workspace string) {
	if action == "" {
		return
	}
	if source == "" {
		source = SourceAPI
	}
	agent = sanitizeAgent(agent, source)
	now := time.Now().UTC()
	u.mu.Lock()
	u.dayFor(u.dayKey(now)).add(action, source, agent, workspace)
	if u.file != nil {
		line, _ := json.Marshal(UsageEvent{TS: now, Action: action, Source: source, Agent: agent, Workspace: workspace})
		line = append(line, '\n')
		_, _ = u.file.Write(line) // OS-buffered; syncLoop fsyncs periodically
	}
	u.mu.Unlock()
}

func (u *UsageTracker) syncLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		u.mu.Lock()
		if u.file != nil {
			_ = u.file.Sync()
		}
		u.mu.Unlock()
	}
}

// Last returns the most recent n days (oldest first), zero-filled so the
// dashboard always gets a continuous series even for quiet days.
func (u *UsageTracker) Last(n int) []DayUsage {
	if n <= 0 {
		n = 30
	}
	if n > usageRetentionDays {
		n = usageRetentionDays
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]DayUsage, 0, n)
	today := time.Now().In(u.loc)
	for i := n - 1; i >= 0; i-- {
		day := today.AddDate(0, 0, -i).Format("2006-01-02")
		if d, ok := u.days[day]; ok {
			cp := *d
			out = append(out, cp)
		} else {
			out = append(out, DayUsage{Date: day, BySource: map[string]int{}})
		}
	}
	return out
}

// ClassifyUsage maps a request to a usage action. log=false means "don't
// record" — inventory/telemetry reads the dashboard polls every few seconds,
// which would drown out real agent signal.
func ClassifyUsage(method, path string) (action string, log bool) {
	// Telemetry — never log (dashboard polls these constantly).
	switch path {
	case "/api/v1/health", "/health", "/",
		"/api/v1/metrics",
		"/api/v1/usage/daily",
		"/api/v1/messages/analytics":
		return "", false
	}
	// Retrieval = search-family.
	if strings.HasPrefix(path, "/api/v1/messages/search") ||
		strings.HasSuffix(path, "/search") && strings.Contains(path, "/workspaces/") ||
		strings.HasSuffix(path, "/conclusions/query") ||
		strings.HasSuffix(path, "/conclusions/trace") ||
		strings.HasSuffix(path, "/conclusions/stale") ||
		strings.HasSuffix(path, "/conclusions/brief") ||
		strings.HasSuffix(path, "/representations") ||
		strings.HasSuffix(path, "/messages/grep") ||
		strings.HasSuffix(path, "/messages/temporal") ||
		strings.HasSuffix(path, "/code/symbols") ||
		strings.HasSuffix(path, "/code/callers") ||
		strings.Contains(path, "/context") && method == "GET" {
		return UsageSearch, true
	}
	switch {
	case path == "/api/v1/messages" && method == "POST",
		path == "/api/v1/messages/batch" && method == "POST":
		return UsageStore, true
	case path == "/api/v1/messages/upload" && method == "POST":
		return UsageUpload, true
	case strings.HasSuffix(path, "/brain/ask") && method == "POST":
		return UsageAsk, true
	case strings.Contains(path, "/chat") && (method == "POST" || method == "GET"):
		return UsageChat, true
	case strings.HasPrefix(path, "/api/v1/code/"):
		return UsageCode, true
	}
	// Any other write is still usage (session create, peer setup, keys…).
	if method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE" {
		return UsageOther, true
	}
	return "", false
}
