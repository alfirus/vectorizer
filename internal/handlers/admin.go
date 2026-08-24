package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/embedding"
	"github.com/alfirus/vectorizer/internal/llmbrain"
)

type AdminHandler struct {
	embed *embedding.Service
	brain *llmbrain.Service
}

func NewAdminHandler(embed *embedding.Service, brain *llmbrain.Service) *AdminHandler {
	return &AdminHandler{embed: embed, brain: brain}
}

// POST /admin/embedding - hot-swap embedding model without restart (Phase 5)
// Body: {model: "nomic-embed-text", base_url?: "..."}
func (h *AdminHandler) SetEmbedding(c *fiber.Ctx) error {
	var req struct {
		Model   string `json:"model"`
		BaseURL string `json:"base_url"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid body"})
	}
	if req.Model != "" {
		h.embed.SetModel(req.Model)
	}
	if req.BaseURL != "" {
		h.embed.SetBaseURL(req.BaseURL)
	}
	return c.JSON(fiber.Map{"model": h.embed.Model(), "hot_swapped": true})
}

func (h *AdminHandler) GetEmbedding(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"model": h.embed.Model()})
}
