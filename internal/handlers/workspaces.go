package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

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
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid workspace ID"})
	}
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

	// Get embedding model info
	embeddingModel := "unknown"
	embeddingDim := 0
	if h.store != nil {
		if info, err := h.store.GetEmbeddingInfo(); err == nil {
			embeddingModel = info["model"].(string)
			embeddingDim = info["dimension"].(int)
		}
	}

	return c.JSON(fiber.Map{
		"workspace_id":    id,
		"document_count":  stats["document_count"],
		"embedding_model": embeddingModel,
		"embedding_dim":   embeddingDim,
		"status":          "healthy",
	})
}
