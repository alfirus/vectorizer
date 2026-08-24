package handlers

import (
	"github.com/gofiber/fiber/v2"

	"github.com/alfirus/vectorizer/internal/llmbrain"
)

// BrainHandler handles optional LLM-powered summarization and Q&A.
type BrainHandler struct {
	brain *llmbrain.Service
}

func NewBrainHandler(brain *llmbrain.Service) *BrainHandler {
	return &BrainHandler{brain: brain}
}

// Summarize summarizes text using the configured LLM.
func (h *BrainHandler) Summarize(c *fiber.Ctx) error {
	if h.brain == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "LLM brain is not enabled",
		})
	}

	var req struct {
		Text       string `json:"text"`
		MaxChars   int    `json:"max_chars,omitempty"`
		WorkspaceID string `json:"workspace_id"` // optional: summarize all messages in workspace
		SessionID  string `json:"session_id"`   // optional: summarize specific session
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Text == "" && (req.WorkspaceID == "" && req.SessionID == "") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "text, or workspace_id/session_id is required",
		})
	}

	var textToSummarize string
	if req.Text != "" {
		textToSummarize = req.Text
	} else if req.WorkspaceID != "" || req.SessionID != "" {
		// TODO: Fetch messages from store and concatenate for summarization
		textToSummarize = "[Workspace/Session messages would be fetched here]"
	}

	resp, err := h.brain.Summarize(llmbrain.SummarizeRequest{
		Text:     textToSummarize,
		MaxChars: req.MaxChars,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to summarize",
		})
	}

	return c.JSON(fiber.Map{
		"summary": resp.Summary,
	})
}

// Ask answers a question about agent memory using the configured LLM.
func (h *BrainHandler) Ask(c *fiber.Ctx) error {
	if h.brain == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "LLM brain is not enabled",
		})
	}

	var req struct {
		Question    string `json:"question"`
		Context     string `json:"context,omitempty"` // optional: pre-fetched context from search
		WorkspaceID string `json:"workspace_id"`      // optional: search workspace for context
		SessionID   string `json:"session_id"`        // optional: search session for context
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Question == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "question is required",
		})
	}

	var context string
	if req.Context != "" {
		context = req.Context
	} else if req.WorkspaceID != "" || req.SessionID != "" {
		// TODO: Search store for relevant context, then pass to LLM
		context = "[Search results would be fetched here]"
	}

	resp, err := h.brain.Ask(req.Question, context)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to answer question",
		})
	}

	return c.JSON(fiber.Map{
		"answer": resp.Answer,
	})
}
