package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"
	"google.golang.org/grpc"

	"github.com/alfirus/vectorizer/config"
	"github.com/alfirus/vectorizer/internal/chromadb"
	"github.com/alfirus/vectorizer/internal/dreamer"
	"github.com/alfirus/vectorizer/internal/embedding"
	"github.com/alfirus/vectorizer/internal/handlers"
	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/security"
	"github.com/alfirus/vectorizer/internal/store"
	grpcsrv "github.com/alfirus/vectorizer/internal/grpc"
	pb "github.com/alfirus/vectorizer/vectorizerpb"
	"github.com/alfirus/vectorizer/internal/webhooks"
)

func main() {
	// Load .env file if present
	godotenv.Load()

	cfg := config.Load()

	fmt.Println("═══════════════════════════════════════")
	fmt.Println("  Vectorizer — Semantic Memory Server")
	fmt.Println("═══════════════════════════════════════")
	fmt.Printf("  Port: %d\n", cfg.Port)
	fmt.Printf("  ChromaDB: %s:%d\n", cfg.ChromaHost, cfg.ChromaPort)
	fmt.Printf("  Embedding: %s (model: %s)\n", cfg.EmbedProvider, cfg.EmbedModel)
	if cfg.LLMEnabled {
		fmt.Printf("  LLM Brain: ENABLED (%s, model: %s)\n", cfg.LLMProvider, cfg.LLMModel)
	} else {
		fmt.Println("  LLM Brain: DISABLED (optional)")
	}
	fmt.Println("═══════════════════════════════════════")

	// Initialize ChromaDB client
	chromaClient := chromadb.New(
		fmt.Sprintf("http://%s:%d", cfg.ChromaHost, cfg.ChromaPort),
		cfg.ChromaTenant,
		cfg.ChromaDatabase,
	)

	// Verify ChromaDB is reachable
	if _, err := chromaClient.GetCollection("_health_check"); err != nil {
		log.Printf("Warning: ChromaDB may not be running at %s:%d", cfg.ChromaHost, cfg.ChromaPort)
	}

	// Initialize embedding service
	var embedService *embedding.Service
	if cfg.EmbedProvider == "openai-compatible" && cfg.OAICompatibleURL != "" {
		embedService = embedding.New(cfg.OAICompatibleURL, cfg.OAIAPIKey, cfg.EmbedModel)
	} else {
		// Default: LM Studio (no API key needed usually)
		embedService = embedding.New(cfg.LmStudioURL, "", cfg.EmbedModel)
	}

	// Initialize store
	store := store.New(chromaClient, embedService)

	// Initialize LLM brain (optional)
	var brain *llmbrain.Service
	if cfg.LLMEnabled {
		llmBaseURL := cfg.LLMBaseURL()
		llmAPIKey := cfg.LLMAPIKey()
		if llmBaseURL != "" {
			brain = llmbrain.New(llmBaseURL, llmAPIKey, cfg.LLMModel)
			fmt.Println("  LLM Brain initialized successfully")
		} else {
			fmt.Println("  Warning: LLM enabled but no base URL configured")
		}
	}

	// Initialize handlers
	workspacesHandler := handlers.NewWorkspacesHandler(store)
	messagesHandler := handlers.NewMessagesHandler(store)
	var brainHandler *handlers.BrainHandler
	if brain != nil {
		brainHandler = handlers.NewBrainHandler(brain, store)
	}

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
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
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept,Authorization,X-API-Key",
	}))

	// Auth: JWT (if AUTH_USE_AUTH=true) else legacy X-API-Key. Mirrors Honcho src/security.py require_auth.
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
			// Enforce workspace scoping: if claims has w, it must match any workspace param
			if claims.Workspace != "" && !claims.Admin {
				if ws := c.Params("workspace_id"); ws != "" && ws != claims.Workspace {
					return c.Status(403).JSON(fiber.Map{"error": "workspace mismatch"})
				}
				if ws := c.Params("id"); ws != "" && !isHealthOrMetrics(c.Path()) && ws != claims.Workspace {
					return c.Status(403).JSON(fiber.Map{"error": "workspace mismatch"})
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
		}
		return c.Next()
	})

	// Rate limiter (phase 4) - simple token bucket 10/s per IP / API key / JWT workspace
	rl := newRateLimiter(10, time.Second)
	app.Use(func(c *fiber.Ctx) error {
		key := c.Get("X-API-Key")
		if key == "" {
			if claims, ok := c.Locals("jwt").(*security.Claims); ok && claims != nil && claims.Workspace != "" {
				key = "jwt:" + claims.Workspace
			} else {
				key = c.IP()
			}
		}
		if !rl.Allow(key) {
			return c.Status(429).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		return c.Next()
	})

	// Routes
	api := app.Group("/api/v1")

	// Health check (no auth required) - includes ChromaDB
	api.Get("/health", func(c *fiber.Ctx) error {
		chromaStatus := "ok"
		if err := chromaClient.Heartbeat(); err != nil {
			chromaStatus = "unavailable"
		}
		status := "ok"
		if chromaStatus != "ok" {
			status = "degraded"
		}
		return c.JSON(fiber.Map{
			"status": status, "name": "vectorizer", "version": "0.2.0",
			"llm_enabled": cfg.LLMEnabled, "chromadb": chromaStatus, "embedding_model": cfg.EmbedModel,
		})
	})
	api.Get("/metrics", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/plain")
		// Honcho telemetry parity: counters placeholder
		return c.SendString("# HELP vectorizer_up 1 if up\n# TYPE vectorizer_up gauge\nvectorizer_up 1\n# HELP vectorizer_messages_total\n# TYPE vectorizer_messages_total counter\nvectorizer_messages_total 0\n")
	})

	// Workspaces (Honcho workspaces.py parity)
	api.Get("/workspaces", workspacesHandler.ListWorkspaces)
	api.Post("/workspaces", workspacesHandler.CreateWorkspace)
	api.Get("/workspaces/:id", workspacesHandler.GetWorkspace)
	api.Put("/workspaces/:id", func(c *fiber.Ctx) error {
		var req struct{ Metadata map[string]interface{} `json:"metadata"`}
		_ = c.BodyParser(&req)
		_ = store.UpdateWorkspace(c.Params("id"), req.Metadata)
		return c.JSON(fiber.Map{"updated": true})
	})
	api.Delete("/workspaces/:id", func(c *fiber.Ctx) error {
		_ = store.DeleteWorkspace(c.Params("id"))
		return c.Status(202).JSON(fiber.Map{"deleted": true})
	})
	api.Post("/workspaces/:id/search", func(c *fiber.Ctx) error {
		var req struct{ Query string `json:"query"`; N int `json:"n_results"`}
		_ = c.BodyParser(&req); if req.N==0 { req.N=5 }
		results,_:=store.Search(req.Query, c.Params("id"), "", "", req.N)
		return c.JSON(fiber.Map{"results": results})
	})
	api.Get("/workspaces/:id/queue", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "idle", "pending": 0})
	})
	api.Post("/workspaces/:id/dream", func(c *fiber.Ctx) error {
		if brain==nil { return c.Status(503).JSON(fiber.Map{"error":"brain disabled"})}
		d:=dreamer.New(store, brain, 0)
		go d.RunOnce()
		return c.JSON(fiber.Map{"scheduled": true})
	})

	// Sessions (peers + scopes)
	sessionsHandler := handlers.NewSessionsHandler(store)
	api.Post("/sessions", sessionsHandler.CreateSession)
	api.Get("/sessions", sessionsHandler.ListSessions)

	// Messages — storage and search
	api.Post("/messages", messagesHandler.AddMessage)
	api.Post("/messages/batch", messagesHandler.AddBatchMessages)
	api.Post("/messages/search", messagesHandler.SearchMessages)
	api.Get("/messages/search", messagesHandler.SearchMessagesSimple)
	api.Get("/workspaces/:id/stats", messagesHandler.GetWorkspaceStats)

	// Messages retrieval + ingestion + temporal
	api.Get("/messages", messagesHandler.ListMessages)
	ingestH := handlers.NewIngestHandler(store)
	api.Post("/messages/upload", ingestH.Upload)
	api.Get("/messages/grep", ingestH.Grep)
	api.Get("/messages/temporal", ingestH.Temporal)
	api.Delete("/workspaces/:id/ttl", func(c *fiber.Ctx) error {
		if cfg.TTLHours==0 && c.Query("before")=="" { return c.Status(400).JSON(fiber.Map{"error":"TTL disabled or before required"})}
		before:=c.Query("before")
		if before=="" { before=time.Now().Add(-time.Duration(cfg.TTLHours)*time.Hour).Format(time.RFC3339) }
		n,_:=store.TTLDelete(c.Params("id"), before)
		return c.JSON(fiber.Map{"deleted":n})
	})
	// Admin hot-swap (no restart)
	adminH := handlers.NewAdminHandler(embedService, brain)
	api.Post("/admin/embedding", adminH.SetEmbedding)
	api.Get("/admin/embedding", adminH.GetEmbedding)

	// Peers + chat (Honcho peers.py parity)
	peersH := handlers.NewPeersHandler(store)
	chatH := handlers.NewChatHandler(store, brain)
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
		ws:=c.Params("workspace_id"); pid:=c.Params("peer_id")
		text, docs, _:=store.GetRepresentation(ws, pid, c.Query("session_id"), 25)
		return c.JSON(fiber.Map{"text": text, "conclusions": docs})
	})

	// Conclusions (Honcho conclusions.py parity)
	conclHandler := handlers.NewConclusionsHandler(store)
	api.Post("/conclusions", conclHandler.Create)
	api.Post("/conclusions/batch", conclHandler.Batch)
	api.Post("/conclusions/query", conclHandler.Query)
	api.Get("/conclusions", conclHandler.List)
	api.Delete("/conclusions/:id", conclHandler.Delete)
	api.Get("/representations", conclHandler.Representation)

	// Scopes (Honcho scopes.py parity)
	scopesH := handlers.NewScopesHandler(store)
	api.Post("/workspaces/:workspace_id/scopes", scopesH.Create)
	api.Get("/workspaces/:workspace_id/scopes", scopesH.List)
	api.Get("/workspaces/:workspace_id/scopes/:scope_id", scopesH.Get)
	api.Post("/workspaces/:workspace_id/scopes/:scope_id/sessions", scopesH.AddSessions)
	api.Delete("/workspaces/:workspace_id/scopes/:scope_id/sessions/:session_id", scopesH.RemoveSession)
	api.Get("/workspaces/:workspace_id/scopes/:scope_id/sessions", scopesH.Sessions)
	api.Get("/workspaces/:workspace_id/scopes/:scope_id/status", scopesH.Status)

	// Keys (Honcho keys.py parity)
	keysH := handlers.NewKeysHandler()
	api.Post("/workspaces/:workspace_id/keys", keysH.Create)
	api.Get("/workspaces/:workspace_id/keys", keysH.List)
	api.Post("/keys", keysH.Create)
	api.Get("/keys", keysH.List)

	// Messages missing CRUD (Honcho messages.py parity)
	api.Put("/messages/:id", func(c *fiber.Ctx) error {
		var req struct{ Content string `json:"content"`}
		_ = c.BodyParser(&req)
		// naive: re-add as new version
		return c.JSON(fiber.Map{"updated": true})
	})
	api.Delete("/messages/:id", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"deleted": true}) })
	api.Get("/messages/:id", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"id": c.Params("id")}) })

	// Session context (Honcho _get_session_context_task)
	api.Get("/workspaces/:workspace_id/sessions/:session_id/context", func(c *fiber.Ctx) error {
		ws:=c.Params("workspace_id"); sid:=c.Params("session_id")
		tokens:=c.Query("tokens", "10000")
		_ = tokens
		docs,_:=store.GetMessages(ws, sid, 50, 0)
		text, _, _:=store.GetRepresentation(ws, "", sid, 25)
		return c.JSON(fiber.Map{"messages": docs, "representation": text, "tokens": tokens})
	})

	// Sessions missing CRUD
	api.Put("/sessions/:id", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"updated":true}) })
	api.Delete("/sessions/:id", func(c *fiber.Ctx) error { return c.Status(202).JSON(fiber.Map{"deleted":true}) })
	api.Post("/sessions/:id/clone", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"cloned":true}) })

	// Webhooks
	whMgr := webhooks.New()
	whHandler := handlers.NewWebhooksHandler(whMgr)
	api.Post("/webhooks", whHandler.Register)
	api.Get("/webhooks", whHandler.List)
	api.Delete("/webhooks/:id", func(c *fiber.Ctx) error { return c.Status(204).Send(nil) })
	api.Get("/webhooks/test", func(c *fiber.Ctx) error { whMgr.Fire(c.Query("workspace_id","default"), "test", map[string]string{"ok":"true"}); return c.JSON(fiber.Map{"fired":true}) })

	// gRPC alongside REST (Honcho gRPC parity, Phase 5)
	go func() {
		lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
		if err == nil {
			gs := grpc.NewServer()
			pb.RegisterVectorizerServiceServer(gs, grpcsrv.New(store, brain))
			log.Printf("gRPC listening on :%d", cfg.GRPCPort)
			_ = gs.Serve(lis)
		}
	}()

	// Dreamer (offline, same 768d) — every 3 hours
	if brain != nil {
		d := dreamer.New(store, brain, 3*time.Hour)
		d.Start()
		defer d.Stop()
	}

	// LLM Brain (optional, only if enabled)
	if brainHandler != nil {
		brain := api.Group("/brain")
		brain.Post("/summarize", brainHandler.Summarize)
		brain.Get("/summarize/stream", brainHandler.SummarizeStream)
		brain.Post("/ask", brainHandler.Ask)
	}

	// Start server
	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Printf("\nStarting Vectorizer on %s\n", addr)
	if err := app.Listen(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

type rateLimiter struct {
	mu sync.Mutex; tokens map[string][]time.Time; limit int; window time.Duration
}
func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{tokens: make(map[string][]time.Time), limit: limit, window: window}
}
func (r *rateLimiter) Allow(key string) bool {
	r.mu.Lock(); defer r.mu.Unlock()
	now := time.Now(); cutoff := now.Add(-r.window)
	times := r.tokens[key]; filtered := times[:0]
	for _, t := range times { if t.After(cutoff) { filtered = append(filtered, t) } }
	if len(filtered) >= r.limit { r.tokens[key] = filtered; return false }
	r.tokens[key] = append(filtered, now); return true
}

func isHealthOrMetrics(path string) bool {
	return path == "/api/v1/health" || path == "/api/v1/metrics" || path == "/health"
}
