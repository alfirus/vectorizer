package handlers

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/models"
	"github.com/alfirus/vectorizer/internal/store"
)

// scoreOf returns the best (highest) score in a result set, 0 when empty.
func scoreOf(results []models.SearchResult) float64 {
	best := 0.0
	for _, r := range results {
		if r.Score > best {
			best = r.Score
		}
	}
	return best
}

type BrainHandler struct {
	brain *llmbrain.Service
	store *store.Store
}

func NewBrainHandler(brain *llmbrain.Service, s *store.Store) *BrainHandler {
	return &BrainHandler{brain: brain, store: s}
}

func (h *BrainHandler) Summarize(c *fiber.Ctx) error {
	if h.brain == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "LLM brain is not enabled"})
	}
	var req struct {
		Text        string `json:"text"`
		MaxChars    int    `json:"max_chars,omitempty"`
		WorkspaceID string `json:"workspace_id"`
		SessionID   string `json:"session_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	text := req.Text
	if text == "" && (req.WorkspaceID != "" || req.SessionID != "") {
		docs, err := h.store.GetMessages(req.WorkspaceID, req.SessionID, 50, 0)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch messages"})
		}
		var parts []string
		for _, d := range docs {
			if doc, ok := d["document"].(string); ok {
				parts = append(parts, doc)
			}
		}
		text = strings.Join(parts, "\n")
		if text == "" {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "no messages found"})
		}
	}
	if text == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "text, or workspace_id/session_id is required"})
	}
	resp, err := h.brain.Summarize(llmbrain.SummarizeRequest{Text: text, MaxChars: req.MaxChars})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to summarize"})
	}
	return c.JSON(fiber.Map{"summary": resp.Summary})
}

func (h *BrainHandler) SummarizeStream(c *fiber.Ctx) error {
	if h.brain == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "LLM brain is not enabled"})
	}
	text := c.Query("text")
	if text == "" {
		text = c.Query("q")
	}
	// fallback to body if needed - simplified: use query param for SSE
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	resp, err := h.brain.Summarize(llmbrain.SummarizeRequest{Text: text})
	if err != nil {
		return c.SendString("data: error\n\n")
	}
	return c.SendString("data: " + resp.Summary + "\n\n")
}

func (h *BrainHandler) Ask(c *fiber.Ctx) error {
	if h.brain == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "LLM brain is not enabled"})
	}
	var req struct {
		Question    string `json:"question"`
		Context     string `json:"context,omitempty"`
		WorkspaceID string `json:"workspace_id"`
		SessionID   string `json:"session_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Question == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "question is required"})
	}
	ctx := req.Context
	if ctx == "" && (req.WorkspaceID != "" || req.SessionID != "") {
		results, err := h.store.Search(req.Question, req.WorkspaceID, req.SessionID, "", 5)
		if err == nil {
			// Relevance floor: never build context from noise. Chroma cosine
			// distance above the floor means "no good hit" — abstain instead of
			// letting the LLM confabulate from junk (e.g. identity docs in the
			// wrong workspace). Shared floor with chat auto-store (minRelevantScore,
			// tunable via RAG_MIN_SCORE).
			minScore := minRelevantScore()
			var parts []string
			for _, r := range results {
				if r.Score >= minScore {
					parts = append(parts, r.Document)
				}
			}
			if len(parts) == 0 {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
					"error":          "no relevant context found",
					"best_score":     scoreOf(results),
					"min_score":      minScore,
					"suggestion":     "try another workspace, rephrase, or reindex the vault",
				})
			}
			ctx = strings.Join(parts, "\n")
		}
	}
	resp, err := h.brain.Ask(req.Question, ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to answer question"})
	}
	return c.JSON(fiber.Map{"answer": resp.Answer})
}
