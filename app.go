package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"

	"github.com/alfirus/vectorizer/config"
	"github.com/alfirus/vectorizer/internal/chromadb"
	"github.com/alfirus/vectorizer/internal/deriver"
	"github.com/alfirus/vectorizer/internal/dreamer"
	"github.com/alfirus/vectorizer/internal/embedding"
	"github.com/alfirus/vectorizer/internal/handlers"
	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/security"
	"github.com/alfirus/vectorizer/internal/store"
	"github.com/alfirus/vectorizer/internal/version"
	"github.com/alfirus/vectorizer/internal/webhooks"
	"github.com/alfirus/vectorizer/internal/writeback"
)

// rateLimitKeyLocal carries the validated identity the auth middleware
// established for this request (see the V2 note at the limiter below).
const rateLimitKeyLocal = "rate_limit_key"

// appDeps is everything buildApp needs to wire the HTTP surface. Tests
// inject fakes (unreachable Chroma, stub embedder, tiny limiter) and get
// the real middleware + route table back.
type appDeps struct {
	cfg      *config.Config
	chroma   *chromadb.Client
	embed    embedding.Embedder
	vecStore *store.Store
	brain    *llmbrain.Service
	drv      *deriver.Deriver
	wb       *writeback.Writer
	whMgr    *webhooks.Manager
	rl       *rateLimiter
}

// buildApp wires middleware + routes and returns the Fiber app without
// starting it. main() calls it once; tests call it with fakes.
func buildApp(d *appDeps) *fiber.App {
	cfg := d.cfg
	vecStore := d.vecStore
	chromaClient := d.chroma
	embedService := d.embed
	brain := d.brain
	drv := d.drv
	wb := d.wb
	whMgr := d.whMgr
	// Same fail-closed predicate main() gates startup on: /health reports
	// whether the server is actually authenticating anything.
	authConfigured := cfg.DefaultAPIKey != "" || (cfg.AuthUseAuth && cfg.AuthJWTSecret != "")

	// Initialize handlers
	workspacesHandler := handlers.NewWorkspacesHandler(vecStore)
	messagesHandler := handlers.NewMessagesHandler(vecStore)
	if drv != nil {
		messagesHandler.SetDeriver(drv)
	}
	if wb != nil {
		messagesHandler.SetWriteback(wb)
	}
	messagesHandler.SetWebhooks(whMgr)
	var brainHandler *handlers.BrainHandler
	if brain != nil {
		brainHandler = handlers.NewBrainHandler(brain, vecStore)
	}

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		// Immutable: Fiber's Params()/Get()/Query() hand out unsafe strings
		// that alias the pooled ctx path/header buffers when Immutable is off
		// (the default). Anything stored past the request then changes when
		// the next request reuses that buffer — t_7c865ae4 caught a queued
		// message_id mutating from "deadbeef-…" to "pending2-…" that way and
		// the replay failing with "message not found". Fiber's own docs on
		// these getters say: "Returned value is only valid within the handler.
		// Do not store any references. Make copies or use the Immutable
		// setting instead." We pay one small copy per string.
		Immutable: true,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{
				"error": err.Error(),
			})
		},
	})

	// Middleware
	app.Use(logger.New())
	// CORS: allow localhost + tailnet + LAN. Vectorizer is API-only; dashboard proxies via /api/vectorizer so this is for direct API callers + mobile.
	// If CORS_ALLOWED_ORIGINS env is set, use it; else default to tailnet-friendly permissive regex.
	allowedOrigins := "*"
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		allowedOrigins = v
	}
	app.Use(cors.New(cors.Config{
		AllowOrigins: allowedOrigins,
		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept,Authorization,X-API-Key,X-Source,X-Agent",
	}))

	// Auth: JWT (if AUTH_USE_AUTH=true) else legacy X-API-Key. Workspace-scoped JWT auth.
	app.Use(func(c *fiber.Ctx) error {
		path := c.Path()
		if path == "/api/v1/health" || path == "/api/v1/metrics" || path == "/health" || path == "/" || c.Method() == "OPTIONS" {
			return c.Next()
		}
		if cfg.AuthUseAuth && cfg.AuthJWTSecret != "" {
			token := security.ExtractBearer(c.Get("Authorization"))
			if token == "" {
				// also accept X-API-Key as bearer for compat
				token = c.Get("X-API-Key")
			}
			if token == "" {
				return c.Status(401).JSON(fiber.Map{"error": "missing bearer token"})
			}
			claims, err := security.ParseToken(cfg.AuthJWTSecret, token)
			if err != nil {
				return c.Status(401).JSON(fiber.Map{"error": "invalid token"})
			}
			c.Locals("jwt", claims)
			// V2: bind the limiter bucket to the validated JWT identity here —
			// the limiter below must never derive buckets from raw headers.
			jwtSubject := "unscoped"
			if claims.Admin {
				jwtSubject = "admin"
			} else if claims.Workspace != "" {
				jwtSubject = claims.Workspace
			}
			c.Locals(rateLimitKeyLocal, "jwt:"+jwtSubject)
			// Enforce workspace scoping: a scoped non-admin claim must match
			// the workspace carried by the route. Resolution order is
			// param -> query; JSON-body workspaces are enforced per-handler
			// (see handlers.requireWorkspace) because the body is not parsed
			// at middleware time. Non-workspace :id params (message, session,
			// conclusion, webhook IDs) are NOT compared here — comparing them
			// 403s every correctly-scoped CRUD call (F2). The only :id that
			// names a workspace is /workspaces/:id, enforced below.
			if claims.Workspace != "" && !claims.Admin {
				if ws := c.Params("workspace_id"); ws != "" && ws != claims.Workspace {
					return c.Status(403).JSON(fiber.Map{"error": "workspace mismatch"})
				}
				if ws := c.Query("workspace_id"); ws != "" && ws != claims.Workspace {
					return c.Status(403).JSON(fiber.Map{"error": "workspace mismatch"})
				}
				if strings.HasPrefix(path, "/api/v1/workspaces/") {
					if ws := c.Params("id"); ws != "" && ws != claims.Workspace {
						return c.Status(403).JSON(fiber.Map{"error": "workspace mismatch"})
					}
				}
			}
			return c.Next()
		}
		if cfg.DefaultAPIKey != "" {
			apiKey := c.Get("X-API-Key")
			if apiKey == "" {
				return c.Status(401).JSON(fiber.Map{"error": "missing API key"})
			}
			if apiKey != cfg.DefaultAPIKey {
				return c.Status(403).JSON(fiber.Map{"error": "invalid API key"})
			}
			// V2: bucket on a hash of the VERIFIED key, never the raw header.
			sum := sha256.Sum256([]byte(apiKey))
			c.Locals(rateLimitKeyLocal, "apikey:"+hex.EncodeToString(sum[:])[:16])
		}
		return c.Next()
	})

	// Rate limiter (phase 4) - 50/s for validated identity (vault reindex
	// burst), 10/s IP fallback for open mode. V2: the bucket key comes ONLY
	// from the auth middleware's validated-identity local — never from a raw
	// header. (In JWT mode X-API-Key is not even inspected by auth, so keying
	// on it gave every JWT holder a fresh 50/s bucket per garbage header.)
	rl := d.rl
	if rl == nil {
		rl = newRateLimiter(50, time.Second)
	}
	app.Use(func(c *fiber.Ctx) error {
		key, _ := c.Locals(rateLimitKeyLocal).(string)
		limit := 50
		if key == "" {
			key = "ip:" + c.IP()
			limit = 10
		}
		if !rl.AllowWithLimit(key, limit) {
			return c.Status(429).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		return c.Next()
	})

	// Usage logger — answers "is any AI agent really using Vectorizer?".
	// Runs right after the rate limiter so every allowed call is classified.
	// Telemetry paths (health/metrics/usage/analytics) are never logged,
	// so dashboard polling can't drown out real agent signal.
	app.Use(func(c *fiber.Ctx) error {
		if action, ok := store.ClassifyUsage(c.Method(), c.Path()); ok {
			// Compulsory attribution (REQUIRE_X_AGENT=true): every real agent
			// call must carry its own name. Telemetry paths never reach here
			// (ClassifyUsage excludes them), so dashboard polling is unaffected.
			if cfg.RequireXAgent && strings.TrimSpace(c.Get("X-Agent")) == "" {
				return c.Status(400).JSON(fiber.Map{"error": "missing X-Agent header (agent identity is compulsory)"})
			}
			source := c.Get("X-Source")
			switch source {
			case store.SourceMCP, store.SourceDashboard:
			default:
				source = store.SourceAPI
			}
			ws := c.Query("workspace_id", c.Params("workspace_id", c.Params("id")))
			store.GlobalUsage.Record(action, source, c.Get("X-Agent"), ws)
		}
		return c.Next()
	})

	// Routes
	api := app.Group("/api/v1")

	// Health check (no auth required) - includes ChromaDB + embedding provider.
	// Embedding status is READ from the background probe cache (store.EmbeddingHealth),
	// never probed inline — see the stopEmbedProbe comment above for why.
	api.Get("/health", func(c *fiber.Ctx) error {
		chromaStatus := "ok"
		if err := chromaClient.Heartbeat(); err != nil {
			chromaStatus = "unavailable"
		}
		embed := store.EmbeddingHealth()
		status := "ok"
		if chromaStatus != "ok" || embed.Status == "degraded" {
			status = "degraded"
		}
		authMode := "configured"
		if !authConfigured {
			authMode = "open (no API key/JWT — local-dev only)"
		}
		return c.JSON(fiber.Map{
			"status": status, "name": "vectorizer", "version": version.Version, "auth": authMode,
			"llm_enabled": cfg.LLMEnabled, "chromadb": chromaStatus, "embedding_model": cfg.EmbedModel,
			"embedding": embed.Status, "embedding_error": store.ScrubError(embed.LastErr), "embedding_last_ok": embed.LastOK,
		})
	})
	api.Get("/metrics", func(c *fiber.Ctx) error {
		metrics := store.GlobalMetrics
		c.Set("Content-Type", "text/plain")
		var deriverDrops uint64
		var deriverDepth int
		if drv != nil {
			deriverDrops = drv.Drops()
			deriverDepth = drv.QueueDepth()
		}
		var wbWrites, wbDrops, wbSkippedRO uint64
		var wbDepth int
		if wb != nil {
			wbWrites = wb.Writes()
			wbDrops = wb.Drops()
			wbSkippedRO = wb.SkippedRO()
			wbDepth = wb.QueueDepth()
		}
		return c.SendString(fmt.Sprintf("# HELP vectorizer_up 1 if up\n# TYPE vectorizer_up gauge\nvectorizer_up 1\n# HELP vectorizer_messages_total Total messages added\n# TYPE vectorizer_messages_total counter\nvectorizer_messages_total %d\n# HELP vectorizer_searches_total Total searches\n# TYPE vectorizer_searches_total counter\nvectorizer_searches_total %d\n# HELP vectorizer_deriver_drops_total Facts discarded on full deriver queue\n# TYPE vectorizer_deriver_drops_total counter\nvectorizer_deriver_drops_total %d\n# HELP vectorizer_deriver_queue_depth Pending deriver backlog\n# TYPE vectorizer_deriver_queue_depth gauge\nvectorizer_deriver_queue_depth %d\n# HELP vectorizer_writeback_writes_total Staging markdown appends\n# TYPE vectorizer_writeback_writes_total counter\nvectorizer_writeback_writes_total %d\n# HELP vectorizer_writeback_drops_total Turns lost on full writeback queue\n# TYPE vectorizer_writeback_drops_total counter\nvectorizer_writeback_drops_total %d\n# HELP vectorizer_writeback_skipped_ro_total Turns skipped (vault read-only)\n# TYPE vectorizer_writeback_skipped_ro_total counter\nvectorizer_writeback_skipped_ro_total %d\n# HELP vectorizer_writeback_queue_depth Pending writeback backlog\n# TYPE vectorizer_writeback_queue_depth gauge\nvectorizer_writeback_queue_depth %d\n# HELP vectorizer_pending_updates Deferred PUTs awaiting embed recovery\n# TYPE vectorizer_pending_updates gauge\nvectorizer_pending_updates %d\n# HELP vectorizer_pending_drops_total Queued updates evicted by the inbox bound\n# TYPE vectorizer_pending_drops_total counter\nvectorizer_pending_drops_total %d\n", metrics.MessagesAdded.Load(), metrics.SearchesTotal.Load(), deriverDrops, deriverDepth, wbWrites, wbDrops, wbSkippedRO, wbDepth, store.PendingCount(), store.PendingDrops()))
	})

	// Workspaces ()
	api.Get("/workspaces", workspacesHandler.ListWorkspaces)
	api.Post("/workspaces", workspacesHandler.CreateWorkspace)
	// Health endpoint must be before :id to avoid param matching
	api.Get("/workspaces/:id/health", workspacesHandler.GetWorkspaceHealth)
	api.Get("/workspaces/:id", workspacesHandler.GetWorkspace)
	api.Put("/workspaces/:id", func(c *fiber.Ctx) error {
		var req struct {
			Metadata map[string]interface{} `json:"metadata"`
		}
		_ = c.BodyParser(&req)
		_ = vecStore.UpdateWorkspace(c.Params("id"), req.Metadata)
		return c.JSON(fiber.Map{"updated": true})
	})
	api.Delete("/workspaces/:id", func(c *fiber.Ctx) error {
		if err := vecStore.DeleteWorkspace(c.Params("id")); err != nil {
			return c.Status(500).JSON(fiber.Map{"deleted": false, "error": err.Error()})
		}
		return c.Status(202).JSON(fiber.Map{"deleted": true})
	})
	api.Post("/workspaces/:id/search", func(c *fiber.Ctx) error {
		var req struct {
			Query string `json:"query"`
			N     int    `json:"n_results"`
		}
		_ = c.BodyParser(&req)
		if req.N <= 0 {
			req.N = 5
		}
		if req.N > 100 {
			req.N = 100
		}
		results, err := vecStore.Search(req.Query, c.Params("id"), "", "", req.N)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"results": results})
	})
	api.Get("/workspaces/:id/queue", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "idle", "pending": 0})
	})
	api.Post("/workspaces/:id/dream", func(c *fiber.Ctx) error {
		if brain == nil {
			return c.Status(503).JSON(fiber.Map{"error": "brain disabled"})
		}
		d := dreamer.New(vecStore, brain, 0)
		go d.RunOnce()
		return c.JSON(fiber.Map{"scheduled": true})
	})

	// Sessions (peers + scopes)
	sessionsHandler := handlers.NewSessionsHandler(vecStore)
	api.Post("/sessions", sessionsHandler.CreateSession)
	api.Get("/sessions", sessionsHandler.ListSessions)

	// Messages — storage and search
	api.Post("/messages", messagesHandler.AddMessage)
	api.Post("/messages/batch", messagesHandler.AddBatchMessages)
	api.Post("/messages/search", messagesHandler.SearchMessages)
	api.Get("/messages/search", messagesHandler.SearchMessagesSimple)
	api.Post("/messages/search/all", messagesHandler.SearchAllWorkspaces)
	api.Get("/messages/analytics", messagesHandler.SearchAnalytics)
	api.Get("/usage/daily", func(c *fiber.Ctx) error {
		days, _ := strconv.Atoi(c.Query("days", "30"))
		if days <= 0 || days > 90 {
			days = 30
		}
		return c.JSON(fiber.Map{"days": store.GlobalUsage.Last(days)})
	})
	api.Get("/workspaces/:id/stats", messagesHandler.GetWorkspaceStats)

	// Messages retrieval + ingestion + temporal
	api.Get("/messages", messagesHandler.ListMessages)
	ingestH := handlers.NewIngestHandler(vecStore)
	api.Post("/messages/upload", ingestH.Upload)
	api.Get("/messages/grep", ingestH.Grep)
	api.Get("/messages/temporal", ingestH.Temporal)
	// Codebase indexing mode (phase 2): server-local repo walk → symbol
	// chunks + DEFINES/CALLS/IMPORTS reasoning edges.
	codeH := handlers.NewCodeHandler(vecStore)
	api.Post("/code/index", codeH.Index)
	api.Get("/code/symbols", codeH.Symbols)
	api.Get("/code/callers", codeH.Callers)
	api.Delete("/workspaces/:id/ttl", func(c *fiber.Ctx) error {
		if cfg.TTLHours == 0 && c.Query("before") == "" {
			return c.Status(400).JSON(fiber.Map{"error": "TTL disabled or before required"})
		}
		before := c.Query("before")
		if before == "" {
			before = time.Now().Add(-time.Duration(cfg.TTLHours) * time.Hour).Format(time.RFC3339)
		}
		n, _ := vecStore.TTLDelete(c.Params("id"), before)
		return c.JSON(fiber.Map{"deleted": n})
	})
	// Admin hot-swap (no restart)
	adminH := handlers.NewAdminHandler(embedService, brain)
	api.Post("/admin/embedding", adminH.SetEmbedding)
	api.Get("/admin/embedding", adminH.GetEmbedding)

	// Peers + chat ()
	peersH := handlers.NewPeersHandler(vecStore)
	chatH := handlers.NewChatHandler(vecStore, brain)
	api.Post("/workspaces/:workspace_id/peers", peersH.CreatePeer)
	api.Get("/workspaces/:workspace_id/peers", peersH.ListPeers)
	api.Put("/workspaces/:workspace_id/peers/:peer_id", peersH.UpdatePeer)
	api.Get("/workspaces/:workspace_id/peers/:peer_id/sessions", peersH.Sessions)
	api.Post("/workspaces/:workspace_id/peers/:peer_id/chat", func(c *fiber.Ctx) error {
		return chatH.Chat(c)
	})
	api.Put("/workspaces/:workspace_id/peers/:peer_id/card", peersH.SetPeerCard)
	api.Get("/workspaces/:workspace_id/peers/:peer_id/card", peersH.GetPeerCard)
	api.Post("/workspaces/:workspace_id/chat", chatH.Chat)
	api.Get("/workspaces/:workspace_id/chat/stream", chatH.ChatStream)
	api.Post("/workspaces/:workspace_id/peers/:peer_id/representation", func(c *fiber.Ctx) error {
		ws := c.Params("workspace_id")
		pid := c.Params("peer_id")
		text, docs, _ := vecStore.GetRepresentation(ws, pid, c.Query("session_id"), 25)
		return c.JSON(fiber.Map{"text": text, "conclusions": docs})
	})

	// Conclusions ()
	conclHandler := handlers.NewConclusionsHandler(vecStore)
	api.Post("/conclusions", conclHandler.Create)
	api.Post("/conclusions/batch", conclHandler.Batch)
	api.Post("/conclusions/query", conclHandler.Query)
	api.Get("/conclusions", conclHandler.List)
	api.Delete("/conclusions/:id", conclHandler.Delete)
	api.Get("/representations", conclHandler.Representation)
	// Provenance + hygiene: trace (why-believe / blast-radius), stale
	// (dead-knowledge review proposals), brief (one-shot session overview).
	api.Get("/conclusions/trace", conclHandler.Trace)
	api.Get("/conclusions/stale", conclHandler.Stale)
	api.Get("/conclusions/brief", conclHandler.Brief)

	// Scopes ()
	scopesH := handlers.NewScopesHandler(vecStore)
	api.Post("/workspaces/:workspace_id/scopes", scopesH.Create)
	api.Get("/workspaces/:workspace_id/scopes", scopesH.List)
	api.Get("/workspaces/:workspace_id/scopes/:scope_id", scopesH.Get)
	api.Post("/workspaces/:workspace_id/scopes/:scope_id/sessions", scopesH.AddSessions)
	api.Delete("/workspaces/:workspace_id/scopes/:scope_id/sessions/:session_id", scopesH.RemoveSession)
	api.Get("/workspaces/:workspace_id/scopes/:scope_id/sessions", scopesH.Sessions)
	api.Get("/workspaces/:workspace_id/scopes/:scope_id/status", scopesH.Status)

	// Messages CRUD — forget/correct wrong memories (delete removes all chunks
	// for a message ID; update is delete + re-add under the same ID).
	api.Put("/messages/:id", messagesHandler.UpdateMessageContent)
	api.Delete("/messages/:id", messagesHandler.DeleteMessage)
	// Deferred-update inbox (t_7c865ae4): queued-while-degraded PUTs, replayed
	// on probe recovery. Registered BEFORE /messages/:id on purpose: Fiber
	// matches routes in registration order, so a later /messages/:id would
	// swallow "pending" as an id param.
	api.Get("/messages/pending", messagesHandler.ListPendingUpdates)
	api.Get("/messages/:id", messagesHandler.GetMessageByID)

	// Session context with rolling-window tokens budgeting
	api.Get("/workspaces/:workspace_id/sessions/:session_id/context", func(c *fiber.Ctx) error {
		ws := c.Params("workspace_id")
		sid := c.Params("session_id")
		budget, _ := parseTokens(c.Query("tokens", "10000"))
		docs, _ := vecStore.GetMessages(ws, sid, 100, 0)
		text, _, _ := vecStore.GetRepresentation(ws, "", sid, 25)
		docs, text = vecStore.FitContextWithinTokens(docs, text, budget)
		used := 0
		for _, d := range docs {
			if doc, ok := d["document"].(string); ok {
				used += vecStore.EstimateTokens(doc)
			}
		}
		used += vecStore.EstimateTokens(text)
		return c.JSON(fiber.Map{"messages": docs, "representation": text, "tokens_used": used, "tokens_budget": budget})
	})

	// Sessions missing CRUD
	api.Put("/sessions/:id", func(c *fiber.Ctx) error {
		return c.Status(501).JSON(fiber.Map{"error": "clone/sessions stub — use POST /sessions"})
	})
	api.Delete("/sessions/:id", func(c *fiber.Ctx) error { return c.Status(501).JSON(fiber.Map{"error": "delete not implemented"}) })
	api.Post("/sessions/:id/clone", func(c *fiber.Ctx) error {
		return c.Status(501).JSON(fiber.Map{"error": "clone not implemented — use POST /sessions"})
	})

	// Webhooks (handlers use existing whMgr)
	whHandler := handlers.NewWebhooksHandler(whMgr)
	api.Post("/webhooks", whHandler.Register)
	api.Get("/webhooks", whHandler.List)
	api.Delete("/webhooks/:id", func(c *fiber.Ctx) error { return c.Status(501).JSON(fiber.Map{"error": "delete not implemented"}) })
	api.Get("/webhooks/test", func(c *fiber.Ctx) error {
		whMgr.Fire(c.Query("workspace_id", "default"), "test", map[string]string{"ok": "true"})
		return c.JSON(fiber.Map{"fired": true})
	})

	// LLM Brain (optional, only if enabled)
	if brainHandler != nil {
		brain := api.Group("/brain")
		brain.Post("/summarize", brainHandler.Summarize)
		brain.Get("/summarize/stream", brainHandler.SummarizeStream)
		brain.Post("/ask", brainHandler.Ask)
	}
	return app
}

type rateLimiter struct {
	mu        sync.Mutex
	tokens    map[string][]time.Time
	limit     int
	window    time.Duration
	lastSweep time.Time
}

// sweepInterval bounds how often Allow* scans the whole map for dead keys.
const rateLimiterSweepInterval = time.Minute

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{tokens: make(map[string][]time.Time), limit: limit, window: window, lastSweep: time.Now()}
}

// sweepLocked drops map entries whose timestamps are all expired, so the
// table cannot grow one entry per distinct key forever (F9). Callers must
// hold r.mu. Runs at most once per sweepInterval to keep Allow O(1).
func (r *rateLimiter) sweepLocked(now time.Time) {
	if now.Sub(r.lastSweep) < rateLimiterSweepInterval {
		return
	}
	r.lastSweep = now
	cutoff := now.Add(-r.window)
	for key, times := range r.tokens {
		alive := false
		for _, t := range times {
			if t.After(cutoff) {
				alive = true
				break
			}
		}
		if !alive {
			delete(r.tokens, key)
		}
	}
}

// Size reports the number of tracked keys (for tests/metrics).
func (r *rateLimiter) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.tokens)
}
func (r *rateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.sweepLocked(now)
	cutoff := now.Add(-r.window)
	times := r.tokens[key]
	filtered := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) >= r.limit {
		r.tokens[key] = filtered
		return false
	}
	r.tokens[key] = append(filtered, now)
	return true
}
func (r *rateLimiter) AllowWithLimit(key string, limit int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.sweepLocked(now)
	cutoff := now.Add(-r.window)
	times := r.tokens[key]
	filtered := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) >= limit {
		r.tokens[key] = filtered
		return false
	}
	r.tokens[key] = append(filtered, now)
	return true
}

func parseTokens(s string) (int, error) {
	if v, err := parseInt(s); err == nil {
		return v, nil
	}
	return 10000, nil
}
func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}
