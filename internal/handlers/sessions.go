package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/store"
)

type SessionsHandler struct{ store *store.Store }
func NewSessionsHandler(s *store.Store) *SessionsHandler { return &SessionsHandler{store: s} }

func (h *SessionsHandler) CreateSession(c *fiber.Ctx) error {
	var req struct{ WorkspaceID string `json:"workspace_id"`; SessionID string `json:"session_id"`; Title string `json:"title"`; PeerIDs []string `json:"peer_ids"`; Scope string `json:"scope"`}
	if err:=c.BodyParser(&req); err!=nil { return c.Status(400).JSON(fiber.Map{"error":"invalid body"})}
	if req.WorkspaceID=="" || req.SessionID=="" { return c.Status(400).JSON(fiber.Map{"error":"workspace_id and session_id required"})}
	// Persist peer_ids/scope as a zero-doc marker via metadata (no embedding)
	// Use EnsureWorkspace + store session marker
	if err:=h.store.SaveSessionMeta(req.WorkspaceID, req.SessionID, req.PeerIDs, req.Scope); err!=nil { return c.Status(500).JSON(fiber.Map{"error":"failed to create session"})}
	return c.Status(201).JSON(fiber.Map{"workspace_id":req.WorkspaceID,"session_id":req.SessionID,"peer_ids":req.PeerIDs,"scope":req.Scope})
}
func (h *SessionsHandler) ListSessions(c *fiber.Ctx) error {
	ws:=c.Query("workspace_id")
	if ws=="" { return c.Status(400).JSON(fiber.Map{"error":"workspace_id required"})}
	sessions,_:=h.store.ListSessions(ws)
	return c.JSON(fiber.Map{"sessions":sessions})
}
