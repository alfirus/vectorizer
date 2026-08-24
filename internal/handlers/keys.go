package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/store"
)

type KeysHandler struct{}
func NewKeysHandler() *KeysHandler { return &KeysHandler{} }

func (h *KeysHandler) Create(c *fiber.Ctx) error {
	var req struct{ WorkspaceID string `json:"workspace_id"`; Name string `json:"name"`}
	_ = c.BodyParser(&req)
	if req.WorkspaceID == "" { req.WorkspaceID = c.Params("workspace_id") }
	if req.WorkspaceID == "" { req.WorkspaceID = "default" }
	k := store.GenerateAPIKey(req.WorkspaceID)
	return c.Status(201).JSON(k)
}
func (h *KeysHandler) List(c *fiber.Ctx) error {
	ws := c.Params("workspace_id")
	if ws == "" { ws = c.Query("workspace_id") }
	return c.JSON(fiber.Map{"keys": store.ListKeys(ws)})
}
