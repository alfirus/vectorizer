package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/joho/godotenv"
	"google.golang.org/grpc"

	"github.com/alfirus/vectorizer/config"
	"github.com/alfirus/vectorizer/internal/chromadb"
	"github.com/alfirus/vectorizer/internal/deriver"
	"github.com/alfirus/vectorizer/internal/dreamer"
	"github.com/alfirus/vectorizer/internal/embedding"
	grpcsrv "github.com/alfirus/vectorizer/internal/grpc"
	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/store"
	"github.com/alfirus/vectorizer/internal/webhooks"
	"github.com/alfirus/vectorizer/internal/writeback"
	pb "github.com/alfirus/vectorizer/vectorizerpb"
)

func main() {
	// Load .env file if present
	godotenv.Load()

	cfg := config.Load()

	// P0-2 fail-closed auth: refuse to start open on a non-loopback
	// listener unless the operator explicitly opts into local-dev mode.
	// (The REST listener always binds all interfaces inside the process;
	// Docker port-mapping decides what is reachable. An unset key here
	// means every route — including /code/index and /webhooks — is open.)
	authConfigured := cfg.DefaultAPIKey != "" || (cfg.AuthUseAuth && cfg.AuthJWTSecret != "")
	if !authConfigured && os.Getenv("VECTORIZER_ALLOW_OPEN") != "true" {
		log.Fatalf("refusing to start: no API key or JWT secret configured and VECTORIZER_ALLOW_OPEN != true. " +
			"Set DEFAULT_API_KEY or AUTH_USE_AUTH=true + AUTH_JWT_SECRET, or export VECTORIZER_ALLOW_OPEN=true for loopback-only local dev")
	}
	if !authConfigured {
		log.Printf("WARNING: starting with NO authentication (VECTORIZER_ALLOW_OPEN=true) — bind loopback only, never expose this port")
	}

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

	// Initialize embedding service (768d Nomic default, 1536d Qwen opt-in).
	var embedService embedding.Embedder
	if cfg.EmbedProvider == "google" && cfg.GoogleAPIKey != "" {
		embedService = embedding.NewGoogle(cfg.GoogleAPIKey, cfg.EmbedModel, cfg.EmbedDimensions)
	} else if cfg.EmbedProvider == "openai-compatible" && cfg.OAICompatibleURL != "" {
		embedService = embedding.NewWithDimensions(cfg.OAICompatibleURL, cfg.OAIAPIKey, cfg.EmbedModel, cfg.EmbedDimensions)
	} else if cfg.EmbedProvider == "openai-compatible" {
		// Fallback to LMStudio URL if OAI URL not set but provider is openai-compatible (e.g. local vLLM)
		embedService = embedding.NewWithDimensions(cfg.LmStudioURL, "", cfg.EmbedModel, cfg.EmbedDimensions)
	} else {
		embedService = embedding.NewWithDimensions(cfg.LmStudioURL, "", cfg.EmbedModel, cfg.EmbedDimensions)
	}
	fmt.Printf("  Embedding dimensions: %d\n", cfg.EmbedDimensions)
	// Backpressure guards (slow local models): collapse concurrent duplicate
	// embeds into one in-flight call, cap total in-flight embeds at
	// EMBED_MAX_INFLIGHT so bursts queue in-process, not inside LM Studio.
	embedService = embedding.WrapGate(embedding.WrapDedup(embedService))

	// Initialize store
	vecStore := store.New(chromaClient, embedService)

	// Background embedding probe: /health reports the embedder from a cached
	// result rather than calling it inline. The Docker healthcheck is
	// wget || exit 1 with Timeout=5s, Retries=3 — a synchronous probe against a
	// down LM Studio (EMBED_TIMEOUT_SECS=600) would blow that budget and get
	// Vectorizer restarted by an outage that is not its fault.
	stopEmbedProbe := store.StartEmbeddingProbe(embedService)
	defer stopEmbedProbe()

	// Deferred-update replay (t_7c865ae4): when the probe flips to ok after
	// a degraded window, queued PUTs replay through the write-first store
	// paths. The drain checks the probe again inside the goroutine so a
	// flapping probe cannot replay into a still-dead embedder; mid-replay
	// embed failures re-queue (attempts+1, parked after MaxReplayAttempts),
	// they never destroy the underlying record.
	//
	// Startup race (verified live 2026-10-03): the once-only recovery hook is
	// consumed by the FIRST ok — which on a healthy boot arrives before any
	// outage, draining an empty inbox, so a LATER real degraded→ok
	// transition fires nothing. Belt and suspenders: keep the hook for the
	// degraded-boot case, AND run a periodic reconciler (every probeInterval)
	// that drains whenever the probe is ok. DrainPendingUpdates is
	// single-flight and replay is idempotent (coalesced, write-first), so
	// overlapping passes collapse to wasted no-ops, never double applies.
	// The reconciler replaces nothing: the hook gives prompt replay on the
	// transition tick, the ticker covers hook-consumed/hook-missed cases.
	drainPending := func() {
		hp := store.EmbeddingHealth()
		pending := store.PendingCount()
		// Log only the states worth a line: a healthy "ok/0" tick every 30s
		// is noise. Anything else (backlog waiting, embedder down) stays loud.
		if pending > 0 || hp.Status != "ok" {
			fmt.Printf("drain tick: embed_status=%s pending_count=%d\n", hp.Status, pending)
		}
		if hp.Status != "ok" {
			return
		}
		applied, requeued, failed := store.DrainPendingUpdates(vecStore.ApplyPendingUpdate)
		if applied+requeued+failed > 0 {
			fmt.Printf("pending replay: applied=%d requeued=%d failed=%d remaining=%d\n",
				applied, requeued, failed, store.PendingCount())
		}
	}
	store.SetReplayHook(func() { go drainPending() })
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			drainPending()
		}
	}()

	// Initialize LLM brain (optional)
	var brain *llmbrain.Service
	if cfg.LLMEnabled {
		llmBaseURL := cfg.LLMBaseURL()
		llmAPIKey := cfg.LLMAPIKey()
		if llmBaseURL != "" {
			brain = llmbrain.New(llmBaseURL, llmAPIKey, cfg.LLMModel)
			fmt.Println("  LLM Brain initialized successfully")
			vecStore.SetBrain(brain)
			fmt.Println("  Vault librarian wired: auto-tag + rerank enabled")
		} else {
			fmt.Println("  Warning: LLM enabled but no base URL configured")
		}
	}

	// Initialize deriver (deriver, async conclusions)
	var drv *deriver.Deriver
	if brain != nil {
		drv = deriver.New(vecStore, brain)
		drv.Start()
		defer drv.Stop()
	}
	// Webhooks (created early so messages can fire)
	whMgr := webhooks.New()

	// Vault writeback: mirror stored turns to per-session staging markdown.
	// Gate: VAULT_WRITEBACK=true (default false). Async, never blocks writes.
	var wb *writeback.Writer
	if cfg.VaultWriteback {
		vaultRoot := os.Getenv("VAULT_ROOT")
		if vaultRoot == "" {
			fmt.Println("  Vault writeback: ON but VAULT_ROOT unset — disabled")
		} else {
			wb = writeback.New(vaultRoot, cfg.VaultWritebackWorkspaces)
			wb.Start()
			defer wb.Stop()
			fmt.Printf("  Vault writeback: ON (root=%s", vaultRoot)
			if cfg.VaultWritebackWorkspaces != "" {
				fmt.Printf(", workspaces=%s", cfg.VaultWritebackWorkspaces)
			}
			fmt.Println(")")
		}
	} else {
		fmt.Println("  Vault writeback: OFF (set VAULT_WRITEBACK=true to enable)")
	}

	// HTTP surface (middleware + routes live in app.go so tests can
	// build the real table against fakes).
	app := buildApp(&appDeps{
		cfg: cfg, chroma: chromaClient, embed: embedService, vecStore: vecStore,
		brain: brain, drv: drv, wb: wb, whMgr: whMgr,
	})
	// gRPC alongside REST (gRPC, Phase 5). F1: same auth as REST via a
	// unary interceptor, loopback-only listen, loud failure when the port
	// is taken (previously swallowed — half-up service with no signal).
	go func() {
		grpcAuth := grpcsrv.AuthConfig{
			APIKey:    cfg.DefaultAPIKey,
			JWTSecret: cfg.AuthJWTSecret,
			UseJWT:    cfg.AuthUseAuth,
		}
		bind := os.Getenv("GRPC_BIND")
		if bind == "" {
			bind = "127.0.0.1"
		}
		addr := fmt.Sprintf("%s:%d", bind, cfg.GRPCPort)
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("gRPC listen %s failed: %v", addr, err)
		}
		gs := grpc.NewServer(grpc.UnaryInterceptor(grpcsrv.AuthInterceptor(grpcAuth)))
		pb.RegisterVectorizerServiceServer(gs, grpcsrv.New(vecStore, brain))
		log.Printf("gRPC listening on %s", addr)
		if err := gs.Serve(lis); err != nil {
			log.Fatalf("gRPC serve failed: %v", err)
		}
	}()

	// Dreamer (offline) — every 3 hours
	if brain != nil {
		d := dreamer.New(vecStore, brain, 3*time.Hour)
		d.Start()
		defer d.Stop()
	}

	// Start server
	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Printf("\nStarting Vectorizer on %s\n", addr)
	if err := app.Listen(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
