package main

import (
	"fmt"
	"log"

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
	workspacesHandler := handlers.NewWorkspacesHandler()
	messagesHandler := handlers.NewMessagesHandler(store)
	var brainHandler *handlers.BrainHandler
	if brain != nil {
		brainHandler = handlers.NewBrainHandler(brain)
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
			if path == "/health" || path == "/" || c.Method() == "OPTIONS" {
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

	// Routes
	api := app.Group("/api/v1")

	// Health check (no auth required)
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
			"name":   "vectorizer",
			"version": "0.1.0",
			"llm_enabled": cfg.LLMEnabled,
		})
	})

	// Workspaces
	api.Get("/workspaces", workspacesHandler.ListWorkspaces)
	api.Post("/workspaces", workspacesHandler.CreateWorkspace)
	api.Get("/workspaces/:id", workspacesHandler.GetWorkspace)

	// Messages — storage and search
	api.Post("/messages", messagesHandler.AddMessage)
	api.Post("/messages/batch", messagesHandler.AddBatchMessages)
	api.Post("/messages/search", messagesHandler.SearchMessages)
	api.Get("/messages/search", messagesHandler.SearchMessagesSimple)
	api.Get("/workspaces/:id/stats", messagesHandler.GetWorkspaceStats)

	// LLM Brain (optional, only if enabled)
	if brainHandler != nil {
		brain := api.Group("/brain")
		brain.Post("/summarize", brainHandler.Summarize)
		brain.Post("/ask", brainHandler.Ask)
	}

	// Start server
	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Printf("\nStarting Vectorizer on %s\n", addr)
	if err := app.Listen(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
