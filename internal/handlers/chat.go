package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/models"
	"github.com/alfirus/vectorizer/internal/security"
	"github.com/alfirus/vectorizer/internal/store"
)

type ChatHandler struct {
	store *store.Store
	brain *llmbrain.Service
}

func NewChatHandler(s *store.Store, b *llmbrain.Service) *ChatHandler { return &ChatHandler{store: s, brain: b} }

// POST /workspaces/:workspace_id/chat - dialectic agentic chat (peer chat, tool loop)
func (h *ChatHandler) Chat(c *fiber.Ctx) error {
	if h.brain == nil {
		return c.Status(503).JSON(fiber.Map{"error": "LLM brain not enabled"})
	}
	ws := c.Params("workspace_id")
	var req struct {
		Query          string `json:"query"`
		Observer       string `json:"observer"`
		Observed       string `json:"observed"`
		SessionID      string `json:"session_id"`
		ReasoningLevel string `json:"reasoning_level"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid body"})
	}
	if req.Query == "" { return c.Status(400).JSON(fiber.Map{"error": "query required"})}
	// Strict peer JWT: observer must match JWT p if present
	if claims, ok := c.Locals("jwt").(*security.Claims); ok && claims != nil && claims.Peer != "" && !claims.Admin {
		if req.Observer != "" && req.Observer != claims.Peer {
			return c.Status(403).JSON(fiber.Map{"error": "observer mismatch with JWT"})
		}
		if req.Observer == "" { req.Observer = claims.Peer }
	}
	observer := req.Observer; if observer == "" { observer = req.Observed }
	observed := req.Observed; if observed == "" { observed = observer }
	if observer == "" { observer = "default" ; observed = observer }

	// Initial context: peer cards + representation
	obsCard,_ := h.store.GetPeerCard(ws, observer)
	obsdCard,_ := h.store.GetPeerCard(ws, observed)
	system := llmbrain.AgentSystemPrompt(observer, observed, obsCard, obsdCard)

	level := strings.ToLower(req.ReasoningLevel)
	if level == "" { level = "low" }
	maxTools := map[string]int{"none":0,"low":2,"medium":4,"high":6,"max":8}[level]
	if maxTools==0 && level!="none" { maxTools=2 }
	temperature := map[string]float32{"none":0.1,"low":0.3,"medium":0.5,"high":0.7,"max":0.9}[level]
	if temperature==0 { temperature=0.3 }

	// Seed context via search_memory + representation (preflight), scaled by reasoning_level
	nResults := map[string]int{"none":1,"low":5,"medium":10,"high":15,"max":20}[level]
	if nResults==0 { nResults=5 }
	var seedCtx []string
	if rep, _, _ := h.store.GetRepresentation(ws, observed, req.SessionID, 5); rep != "" {
		seedCtx = append(seedCtx, "Representation:\n"+rep)
	}
	var seedResults []models.SearchResult
	if results, err := h.store.Search(req.Query, ws, req.SessionID, "", nResults); err == nil {
		seedResults = results
		for _, r := range results {
			seedCtx = append(seedCtx, r.Document)
		}
	}
	seedCtxStr := strings.Join(seedCtx, "\n\n")
	if len(seedCtxStr) > 12000 { seedCtxStr = seedCtxStr[:12000] }

	// Build message history for agentic loop
	messages := []map[string]interface{}{
		{"role": "system", "content": system},
		{"role": "user", "content": fmt.Sprintf("Context:\n%s\n\nQuestion: %s", seedCtxStr, req.Query)},
	}

	answer := ""
	var premiseIDs, supportingMsgIDs []string
	for iter := 0; iter <= maxTools; iter++ {
		resp, err := h.brain.ChatWithHistory(messages, temperature)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "chat failed: "+err.Error()})
		}
		toolName, toolArgs := parseToolCall(resp)
		if toolName == "" || iter == maxTools {
			answer = resp
			break
		}
		toolResult := h.execTool(ws, req.SessionID, toolName, toolArgs)
		// Collect premises for reasoning graph
		if toolName == "search_memory" {
			// Try to extract ids from tool result
			var tmp []map[string]interface{}
			_ = json.Unmarshal([]byte(toolResult), &tmp)
			for _, m := range tmp { if id, ok := m["id"].(string); ok { premiseIDs = append(premiseIDs, id) } }
		}
		if toolName == "search_messages" {
			var tmp []map[string]interface{}
			_ = json.Unmarshal([]byte(toolResult), &tmp)
			// store handles SearchResult id -> capture chunk ids
			for _, m := range tmp {
				if id, ok := m["ID"].(string); ok { supportingMsgIDs = append(supportingMsgIDs, id) }
				if id, ok := m["id"].(string); ok { supportingMsgIDs = append(supportingMsgIDs, id) }
			}
		}
		messages = append(messages, map[string]interface{}{"role": "assistant", "content": resp})
		messages = append(messages, map[string]interface{}{"role": "user", "content": fmt.Sprintf("Tool %s result:\n%s\n\nContinue or answer.", toolName, toolResult)})
	}
	// Auto-store chat answer into reasoning graph + assistant message (rolling-window).
	// Confidence gate: only persist when the seed context actually contained a
	// relevant hit (score >= RAG_MIN_SCORE, default 0.22, same floor as Ask).
	// Without this, answers built from junk become immortal wrong "memories"
	// that future queries retrieve (e.g. identity-doc confabulation).
	if answer != "" && req.SessionID != "" && seedRelevant(seedResults) {
		// Persist as conclusion for reasoning grounding
		newID, _ := h.store.CreateConclusion(ws, observed, answer, map[string]interface{}{"source":"chat","observer":observer,"session_id":req.SessionID})
		if newID != "" {
			_ = h.store.AddReasoningEdge(ws, observed, newID, premiseIDs, supportingMsgIDs)
		}
		// Also store as assistant message so next rolling window includes it
		msg := models.NewMessage(ws, req.SessionID, "assistant", answer)
		_ = h.store.AddMessage(msg, answer)
	}
	return c.JSON(fiber.Map{"answer": answer, "observer": observer, "observed": observed})
}

// minRelevantScore is the shared relevance floor (score = 1 - distance,
// nomic-768d): seed hits, Ask context, and chat auto-store all use it.
// Tunable live via RAG_MIN_SCORE; default 0.22.
func minRelevantScore() float64 {
	const def = 0.22
	if v := os.Getenv("RAG_MIN_SCORE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// seedRelevant reports whether the seed search returned at least one hit
// above the relevance floor. Guards chat auto-store: junk-only seeds must
// not become immortal conclusions.
func seedRelevant(results []models.SearchResult) bool {
	floor := minRelevantScore()
	for _, r := range results {
		if r.Score >= floor {
			return true
		}
	}
	return false
}

// filterToolHits drops SearchResult tool hits below the relevance floor.
func filterToolHits(hits []models.SearchResult, floor float64) []models.SearchResult {
	out := hits[:0]
	for _, h := range hits {
		if h.Score >= floor {
			out = append(out, h)
		}
	}
	if out == nil {
		out = []models.SearchResult{}
	}
	return out
}

// filterToolMaps drops QueryConclusions map hits (score key) below the floor.
func filterToolMaps(hits []map[string]interface{}, floor float64) []map[string]interface{} {
	out := hits[:0]
	for _, h := range hits {
		if s, ok := h["score"].(float64); ok && s >= floor {
			out = append(out, h)
		}
	}
	if out == nil {
		out = []map[string]interface{}{}
	}
	return out
}

func parseToolCall(s string) (string, map[string]interface{}) {
	// Extract JSON object containing "tool"
	start := strings.Index(s, `{"tool"`)
	if start == -1 { return "", nil }
	end := strings.LastIndex(s[start:], "}")
	if end == -1 { return "", nil }
	raw := s[start : start+end+1]
	var obj struct{ Tool string `json:"tool"`; Args map[string]interface{} `json:"args"`}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil { return "", nil }
	return obj.Tool, obj.Args
}

func (h *ChatHandler) execTool(ws, sessionID, tool string, args map[string]interface{}) string {
	switch tool {
	case "search_memory":
		q,_:=args["query"].(string)
		scope,_:=args["scope"].(string)
		// scope filtered via peer_id in conclusions metadata if needed; fallback to query
		if scope != "" {
			// future: QueryConclusions with scope; for now search then filter
		}
		res,_:=h.store.QueryConclusions(ws, q, 5)
		// Floor: drop junk conclusions so the tool loop can't build answers
		// from noise. Empty result tells the LLM "nothing relevant" — the
		// abstain signal.
		res = filterToolMaps(res, minRelevantScore())
		b,_:=json.Marshal(res); return string(b)
	case "search_messages":
		q,_:=args["query"].(string)
		scope,_:=args["scope"].(string); peerID,_:=args["peer_id"].(string)
		if sid, ok := args["session_id"].(string); ok && sid != "" { sessionID = sid }
		res,_:=h.store.SearchWithScope(q, ws, sessionID, "", scope, peerID, 5)
		// Same floor for message hits — junk out, abstain signal in.
		res = filterToolHits(res, minRelevantScore())
		b,_:=json.Marshal(res); return string(b)
	case "grep_messages":
		pat,_:=args["pattern"].(string); if pat=="" { pat,_=args["query"].(string) }
		res,_:=h.store.Grep(ws, sessionID, pat, 10)
		b,_:=json.Marshal(res); return string(b)
	case "get_reasoning_chain":
		cid,_:=args["conclusion_id"].(string)
		res,_:=h.store.GetReasoningChain(ws, cid)
		b,_:=json.Marshal(res); return string(b)
	case "get_observation_context":
		cid,_:=args["chunk_id"].(string); sid,_:=args["session_id"].(string); if sid=="" { sid=sessionID }
		res,_:=h.store.GetObservationContext(ws, sid, cid, 2)
		b,_:=json.Marshal(res); return string(b)
	default:
		return "unknown tool"
	}
}

func (h *ChatHandler) ChatStream(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	return h.Chat(c)
}
