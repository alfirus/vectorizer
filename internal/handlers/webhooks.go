package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/webhooks"
)

type WebhooksHandler struct{ m *webhooks.Manager }
func NewWebhooksHandler(m *webhooks.Manager) *WebhooksHandler { return &WebhooksHandler{m:m} }

func (h *WebhooksHandler) Register(c *fiber.Ctx) error {
	var req struct{ WorkspaceID string `json:"workspace_id"`; URL string `json:"url"`; Events []string `json:"events"`}
	if err:=c.BodyParser(&req); err!=nil { return c.Status(400).JSON(fiber.Map{"error":"invalid body"})}
	if req.WorkspaceID=="" || req.URL=="" { return c.Status(400).JSON(fiber.Map{"error":"workspace_id and url required"})}
	h.m.Register(req.WorkspaceID, req.URL, req.Events)
	return c.JSON(fiber.Map{"registered":true})
}
func (h *WebhooksHandler) List(c *fiber.Ctx) error {
	ws:=c.Query("workspace_id")
	return c.JSON(fiber.Map{"endpoints": h.m.List(ws)})
}
