package handlers

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/store"
)

type ChatHandler struct {
	store *store.Store
	brain *llmbrain.Service
}

func NewChatHandler(s *store.Store, b *llmbrain.Service) *ChatHandler { return &ChatHandler{store: s, brain: b} }

// POST /workspaces/:workspace_id/chat - dialectic agentic chat (Honcho peer.chat equivalent)
func (h *ChatHandler) Chat(c *fiber.Ctx) error {
	if h.brain == nil {
		return c.Status(503).JSON(fiber.Map{"error": "LLM brain not enabled"})
	}
	ws := c.Params("workspace_id")
	var req struct {
		Query          string `json:"query"`
		Observer       string `json:"observer"`
		Observed       string `json:"observed"`
		SessionID      string `json:"session_id"`
		ReasoningLevel string `json:"reasoning_level"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid body"})
	}
	if req.Query == "" { return c.Status(400).JSON(fiber.Map{"error": "query required"})}
	observer := req.Observer; if observer == "" { observer = req.Observed }
	observed := req.Observed; if observed == "" { observed = observer }
	if observer == "" { observer = "default" ; observed = observer }

	// Gather context: peer cards + search memory + messages
	var ctxParts []string
	if lines, _ := h.store.GetPeerCard(ws, observed); len(lines)>0 {
		ctxParts = append(ctxParts, fmt.Sprintf("Peer card for %s:\n%s", observed, strings.Join(lines, "\n")))
	}
	if observer != observed {
		if lines, _ := h.store.GetPeerCard(ws, observer); len(lines)>0 {
			ctxParts = append(ctxParts, fmt.Sprintf("Peer card for %s (observer):\n%s", observer, strings.Join(lines, "\n")))
		}
	}
	// search conclusions + messages
	if results, err := h.store.Search(req.Query, ws, req.SessionID, "", 5); err == nil {
		for _, r := range results { ctxParts = append(ctxParts, r.Document) }
	}
	if rep, _, _ := h.store.GetRepresentation(ws, observed, req.SessionID, 5); rep != "" {
		ctxParts = append(ctxParts, "Representation:\n"+rep)
	}
	ctx := strings.Join(ctxParts, "\n\n")
	if len(ctx) > 8000 { ctx = ctx[:8000] }

	level := strings.ToLower(req.ReasoningLevel)
	if level == "" { level = "low" }
	nResults := map[string]int{"none":1,"low":5,"medium":10,"high":15,"max":20}[level]
	if nResults==0 { nResults=5 }
	// If level requests more, redo search with larger k
	if nResults > 5 {
		if results, err := h.store.Search(req.Query, ws, req.SessionID, "", nResults); err == nil {
			ctxParts = ctxParts[:0]
			if lines, _ := h.store.GetPeerCard(ws, observed); len(lines)>0 { ctxParts = append(ctxParts, fmt.Sprintf("Peer card for %s:\n%s", observed, strings.Join(lines, "\n"))) }
			for _, r := range results { ctxParts = append(ctxParts, r.Document) }
			ctx = strings.Join(ctxParts, "\n\n")
			if len(ctx) > 12000 { ctx = ctx[:12000] }
		}
	}
	temperature := map[string]float32{"none":0.1,"low":0.3,"medium":0.5,"high":0.7,"max":0.9}[level]
	system := fmt.Sprintf("You are answering from %s's perspective about %s. Use provided context. If peer cards exist, they are constructed summaries from same observations. Reasoning level: %s.", observer, observed, level)
	resp, err := h.brain.ChatWithTemp(system, ctx, req.Query, temperature)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "chat failed: "+err.Error()})
	}
	return c.JSON(fiber.Map{"answer": resp, "observer": observer, "observed": observed})
}

func (h *ChatHandler) ChatStream(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	return h.Chat(c)
}
