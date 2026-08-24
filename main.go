package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"

	"github.com/alfirus/vectorizer/config"
	"github.com/alfirus/vectorizer/internal/chromadb"
	"github.com/alfirus/vectorizer/internal/embedding"
	"github.com/alfirus/vectorizer/internal/handlers"
	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/store"
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
			return c.Status(500).JSON(fiber.Map{
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

	// API Key middleware (optional)
	if cfg.DefaultAPIKey != "" {
		app.Use(func(c *fiber.Ctx) error {
			apiKey := c.Get("X-API-Key")
			path := c.Path()

			// Skip auth for health check and CORS preflight
			if path == "/api/v1/health" || path == "/health" || path == "/" || c.Method() == "OPTIONS" {
				return c.Next()
			}

			if apiKey == "" {
				return c.Status(401).JSON(fiber.Map{
					"error": "missing API key",
				})
			}

			if apiKey != cfg.DefaultAPIKey {
				return c.Status(403).JSON(fiber.Map{
					"error": "invalid API key",
				})
			}

			return c.Next()
		})
	}

	// Rate limiter (phase 4) - simple token bucket 10/s per IP or API key
	rl := newRateLimiter(10, time.Second)
	app.Use(func(c *fiber.Ctx) error {
		key := c.Get("X-API-Key")
		if key == "" {
			key = c.IP()
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
			"status": status, "name": "vectorizer", "version": "0.1.0",
			"llm_enabled": cfg.LLMEnabled, "chromadb": chromaStatus, "embedding_model": cfg.EmbedModel,
		})
	})
	api.Get("/metrics", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/plain")
		return c.SendString("# HELP vectorizer_up 1 if up\nvectorizer_up 1\n")
	})

	// Workspaces
	api.Get("/workspaces", workspacesHandler.ListWorkspaces)
	api.Post("/workspaces", workspacesHandler.CreateWorkspace)
	api.Get("/workspaces/:id", workspacesHandler.GetWorkspace)

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

	// Messages retrieval
	api.Get("/messages", messagesHandler.ListMessages)

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
