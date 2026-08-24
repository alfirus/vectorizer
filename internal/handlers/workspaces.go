package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/alfirus/vectorizer/internal/models"
)

// WorkspacesHandler handles workspace CRUD operations.
type WorkspacesHandler struct{}

func NewWorkspacesHandler() *WorkspacesHandler {
	return &WorkspacesHandler{}
}

// CreateWorkspace creates a new workspace (namespace for agent memory isolation).
func (h *WorkspacesHandler) CreateWorkspace(c *fiber.Ctx) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "name is required",
		})
	}

	workspace := models.NewWorkspace(req.Name)

	return c.Status(fiber.StatusCreated).JSON(workspace)
}

// ListWorkspaces returns all workspaces.
func (h *WorkspacesHandler) ListWorkspaces(c *fiber.Ctx) error {
	// TODO: Implement workspace persistence (SQLite/Postgres)
	// For now, return empty list
	return c.JSON(fiber.Map{
		"workspaces": []models.Workspace{},
	})
}

// GetWorkspace returns a single workspace by ID.
func (h *WorkspacesHandler) GetWorkspace(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid workspace ID",
		})
	}

	// TODO: Implement workspace lookup from database
	return c.JSON(models.Workspace{
		ID: id,
	})
}
