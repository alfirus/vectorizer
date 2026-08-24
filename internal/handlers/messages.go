package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/alfirus/vectorizer/internal/models"
	"github.com/alfirus/vectorizer/internal/store"
)

// MessagesHandler handles message storage and retrieval.
type MessagesHandler struct {
	store *store.Store
}

func NewMessagesHandler(store *store.Store) *MessagesHandler {
	return &MessagesHandler{store: store}
}

// AddMessage stores a new message with embedding.
func (h *MessagesHandler) AddMessage(c *fiber.Ctx) error {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		SessionID   string `json:"session_id"`
		Role        string `json:"role"` // "user", "assistant", "system"
		Content     string `json:"content"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.WorkspaceID == "" || req.SessionID == "" || req.Role == "" || req.Content == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "workspace_id, session_id, role, and content are required",
		})
	}

	if req.Role != "user" && req.Role != "assistant" && req.Role != "system" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "role must be 'user', 'assistant', or 'system'",
		})
	}

	msg := models.NewMessage(req.WorkspaceID, req.SessionID, req.Role, req.Content)

	if err := h.store.AddMessage(msg, req.Content); err != nil {
		fmt.Printf("Error adding message: %v\n", err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to store message",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":          msg.ID,
		"workspace_id": msg.WorkspaceID,
		"session_id":  msg.SessionID,
		"role":        msg.Role,
		"stored":      true,
	})
}

// AddBatchMessages stores multiple messages in a single request.
func (h *MessagesHandler) AddBatchMessages(c *fiber.Ctx) error {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		Messages []struct {
			WorkspaceID string `json:"workspace_id"`
			SessionID string `json:"session_id"`
			Role      string `json:"role"`
			Content   string `json:"content"`
		} `json:"messages"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if len(req.Messages) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "messages array is required",
		})
	}

	var results []fiber.Map
	for _, msg := range req.Messages {
		if msg.SessionID == "" || msg.Role == "" || msg.Content == "" {
			continue
		}
		wsID := msg.WorkspaceID
		if wsID == "" {
			wsID = req.WorkspaceID
		}
		if wsID == "" {
			results = append(results, fiber.Map{
				"session_id": msg.SessionID,
				"role":       msg.Role,
				"stored":     false,
				"error":      "workspace_id is required",
			})
			continue
		}
		sessionID := msg.SessionID

		m := models.NewMessage(wsID, sessionID, msg.Role, msg.Content)
		if err := h.store.AddMessage(m, msg.Content); err != nil {
			results = append(results, fiber.Map{
				"session_id": sessionID,
				"role":       msg.Role,
				"stored":     false,
				"error":      err.Error(),
			})
			continue
		}

		results = append(results, fiber.Map{
			"id":          m.ID,
			"session_id":  sessionID,
			"role":        msg.Role,
			"stored":      true,
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"results": results,
	})
}

// SearchMessages performs semantic search across messages.
func (h *MessagesHandler) SearchMessages(c *fiber.Ctx) error {
	var req models.SearchRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Query == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "query is required",
		})
	}

	nResults := 10
	if req.NResults > 0 && req.NResults <= 100 {
		nResults = req.NResults
	}

	var wID, sID, role string
	if req.Where != nil {
		if v, ok := req.Where["workspace_id"].(string); ok {
			wID = v
		}
		if v, ok := req.Where["session_id"].(string); ok {
			sID = v
		}
		if v, ok := req.Where["role"].(string); ok {
			role = v
		}
	}
	results, err := h.store.Search(req.Query, wID, sID, role, nResults)
	if err != nil {
		fmt.Printf("Error searching: %v\n", err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to search messages",
		})
	}

	return c.JSON(fiber.Map{
		"results": results,
		"count":   len(results),
	})
}

// SearchMessagesSimple is a simpler search endpoint with query params.
func (h *MessagesHandler) SearchMessagesSimple(c *fiber.Ctx) error {
	query := c.Query("q")
	if query == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "query parameter 'q' is required",
		})
	}

	workspaceID := c.Query("workspace_id")
	sessionID := c.Query("session_id")
	role := c.Query("role")
	nResults, _ := strconv.Atoi(c.Query("n_results", "10"))

	if nResults <= 0 || nResults > 100 {
		nResults = 10
	}

	results, err := h.store.Search(query, workspaceID, sessionID, role, nResults)
	if err != nil {
		fmt.Printf("Error searching: %v\n", err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to search messages",
		})
	}

	return c.JSON(fiber.Map{
		"results": results,
		"count":   len(results),
	})
}

// GetWorkspaceStats returns stats for a workspace.
func (h *MessagesHandler) GetWorkspaceStats(c *fiber.Ctx) error {
	workspaceID := c.Params("id")
	if _, err := strconv.Atoi(strings.TrimPrefix(workspaceID, "ws_")); err != nil && len(workspaceID) > 3 {
		// Might be a UUID or workspace name
	}

	stats, err := h.store.GetWorkspaceStats(workspaceID)
	if err != nil {
		fmt.Printf("Error getting stats: %v\n", err)
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get workspace stats",
		})
	}

	return c.JSON(stats)
}
