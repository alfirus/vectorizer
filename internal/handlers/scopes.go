package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/store"
)

type ScopesHandler struct{ store *store.Store }
func NewScopesHandler(s *store.Store) *ScopesHandler { return &ScopesHandler{store: s} }

func (h *ScopesHandler) Create(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id")
	var req struct{ Name string `json:"name"`; ID string `json:"id"`; Sessions []string `json:"sessions"`}
	if err:=c.BodyParser(&req); err!=nil { return c.Status(400).JSON(fiber.Map{"error":"invalid body"})}
	name:=req.Name; if name=="" { name=req.ID }
	if name=="" { return c.Status(400).JSON(fiber.Map{"error":"name required"})}
	if err:=h.store.CreateScope(ws, name, req.Sessions); err!=nil { return c.Status(500).JSON(fiber.Map{"error":err.Error()})}
	return c.Status(201).JSON(fiber.Map{"id":name, "sessions":req.Sessions})
}
func (h *ScopesHandler) List(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id")
	scopes,_:=h.store.ListScopes(ws)
	return c.JSON(fiber.Map{"scopes":scopes})
}
func (h *ScopesHandler) Get(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id"); id:=c.Params("scope_id")
	sc, err:=h.store.GetScope(ws, id)
	if err!=nil { return c.Status(404).JSON(fiber.Map{"error":"not found"})}
	return c.JSON(sc)
}
func (h *ScopesHandler) AddSessions(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id"); id:=c.Params("scope_id")
	var req struct{ Sessions []string `json:"sessions"`; SessionIDs []string `json:"session_ids"`}
	_ = c.BodyParser(&req)
	sess:=req.Sessions; if len(sess)==0 { sess=req.SessionIDs }
	_ = h.store.AddSessionsToScope(ws, id, sess)
	return c.JSON(fiber.Map{"updated":true})
}
func (h *ScopesHandler) RemoveSession(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id"); id:=c.Params("scope_id"); sid:=c.Params("session_id")
	_ = h.store.RemoveSessionFromScope(ws, id, sid)
	return c.JSON(fiber.Map{"removed":true})
}
func (h *ScopesHandler) Sessions(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id"); id:=c.Params("scope_id")
	sess,_:=h.store.GetScopeSessions(ws, id)
	return c.JSON(fiber.Map{"sessions":sess})
}
func (h *ScopesHandler) Status(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id"); id:=c.Params("scope_id")
	sess,_:=h.store.GetScopeSessions(ws, id)
	return c.JSON(fiber.Map{"scope":id, "session_count":len(sess), "sessions":sess})
}
