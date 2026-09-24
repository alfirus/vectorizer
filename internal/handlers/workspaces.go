package handlers

import (
	"github.com/gofiber/fiber/v2"

	"github.com/alfirus/vectorizer/internal/models"
	"github.com/alfirus/vectorizer/internal/store"
)

type WorkspacesHandler struct{ store *store.Store }

func NewWorkspacesHandler(s *store.Store) *WorkspacesHandler { return &WorkspacesHandler{store: s} }

func (h *WorkspacesHandler) CreateWorkspace(c *fiber.Ctx) error {
	var req struct{ Name string `json:"name"` }
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	req.Name = models.SanitizeString(req.Name)
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
	}
	if !models.ValidateResourceName(req.Name) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid name format (a-zA-Z0-9_-, 1-512 chars)"})
	}
	ws := models.NewWorkspace(req.Name)
	// NewWorkspace canonicalizes name→ID, so the returned ws.ID is the
	// single collection suffix every other path (messages, code, search)
	// already uses. POSTing "PilotV4" twice = same workspace, no dupes.
	if err := h.store.EnsureWorkspace(ws.ID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create workspace"})
	}
	return c.Status(fiber.StatusCreated).JSON(ws)
}

func (h *WorkspacesHandler) ListWorkspaces(c *fiber.Ctx) error {
	ids, err := h.store.ListWorkspaces()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list workspaces"})
	}
	workspaces := make([]models.Workspace, len(ids))
	for i, id := range ids {
		workspaces[i] = models.Workspace{ID: id}
	}
	return c.JSON(fiber.Map{"workspaces": workspaces})
}

func (h *WorkspacesHandler) GetWorkspace(c *fiber.Ctx) error {
	id := models.CanonicalWorkspaceID(c.Params("id"))
	stats, err := h.store.GetWorkspaceStats(id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get workspace"})
	}
	return c.JSON(fiber.Map{"id": id, "stats": stats})
}

// GetWorkspaceHealth returns detailed health info for a workspace.
func (h *WorkspacesHandler) GetWorkspaceHealth(c *fiber.Ctx) error {
	id := c.Params("id")
	stats, err := h.store.GetWorkspaceStats(id)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to get workspace health"})
	}

	// Embedding model info + REAL status. "healthy" used to be hardcoded here,
	// which reported healthy even with the embedder dead — the exact blindness
	// that let a 30-minute LM Studio outage pass unnoticed.
	// GetEmbeddingInfo makes a live embed call, so only take that path when the
	// background probe says the provider answers; otherwise we would block this
	// handler for up to EMBED_TIMEOUT_SECS on an already-dead provider.
	embeddingModel := "unknown"
	embeddingDim := 0
	embed := store.EmbeddingHealth()
	if h.store != nil && embed.Status == "ok" {
		if info, err := h.store.GetEmbeddingInfo(); err == nil {
			embeddingModel = info["model"].(string)
			embeddingDim = info["dimension"].(int)
		}
	} else if embed.Status == "degraded" {
		embeddingModel = store.EmbeddingModelName()
	}

	status := "healthy"
	switch embed.Status {
	case "degraded":
		status = "degraded"
	case "unknown":
		// No probe result yet — do not assert healthy on no evidence.
		status = "unknown"
	}

	return c.JSON(fiber.Map{
		"workspace_id":      id,
		"document_count":    stats["document_count"],
		"embedding_model":   embeddingModel,
		"embedding_dim":     embeddingDim,
		"embedding":         embed.Status,
		"embedding_error":   embed.LastErr,
		"embedding_last_ok": embed.LastOK,
		"status":            status,
	})
}
