package handlers

import (
	"strconv"
	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/store"
)

type ConclusionsHandler struct{ store *store.Store }
func NewConclusionsHandler(s *store.Store) *ConclusionsHandler { return &ConclusionsHandler{store: s} }

func (h *ConclusionsHandler) Create(c *fiber.Ctx) error {
	var req struct{ WorkspaceID string `json:"workspace_id"`; PeerID string `json:"peer_id"`; Content string `json:"content"`; Metadata map[string]interface{} `json:"metadata"`}
	if err:=c.BodyParser(&req); err!=nil { return c.Status(400).JSON(fiber.Map{"error":"invalid body"})}
	if req.WorkspaceID=="" || req.Content=="" { return c.Status(400).JSON(fiber.Map{"error":"workspace_id and content required"})}
	id, err:=h.store.CreateConclusion(req.WorkspaceID, req.PeerID, req.Content, req.Metadata)
	if err!=nil { return c.Status(500).JSON(fiber.Map{"error":"failed to create conclusion"})}
	return c.Status(201).JSON(fiber.Map{"id":id})
}
func (h *ConclusionsHandler) List(c *fiber.Ctx) error {
	ws:=c.Query("workspace_id")
	limit,_:=strconv.Atoi(c.Query("limit","25")); offset,_:=strconv.Atoi(c.Query("offset","0"))
	docs, _:=h.store.ListConclusions(ws, c.Query("peer_id"), limit, offset)
	return c.JSON(fiber.Map{"conclusions":docs})
}
func (h *ConclusionsHandler) Delete(c *fiber.Ctx) error {
	ws:=c.Query("workspace_id"); id:=c.Params("id")
	if err:=h.store.DeleteConclusion(ws,id); err!=nil { return c.Status(500).JSON(fiber.Map{"error":"failed to delete"})}
	return c.JSON(fiber.Map{"deleted":true})
}
func (h *ConclusionsHandler) Representation(c *fiber.Ctx) error {
	ws:=c.Query("workspace_id"); peer:=c.Query("peer_id"); sess:=c.Query("session_id")
	max,_:=strconv.Atoi(c.Query("max_conclusions","25"))
	text, docs, _:=h.store.GetRepresentation(ws, peer, sess, max)
	return c.JSON(fiber.Map{"representation":text, "conclusions":docs})
}
func (h *ConclusionsHandler) Batch(c *fiber.Ctx) error {
	var req struct{ Conclusions []struct{ WorkspaceID string `json:"workspace_id"`; PeerID string `json:"peer_id"`; Content string `json:"content"`} `json:"conclusions"`}
	_ = c.BodyParser(&req)
	var ids []string
	for _, concl := range req.Conclusions {
		id,_:=h.store.CreateConclusion(concl.WorkspaceID, concl.PeerID, concl.Content, nil)
		ids=append(ids, id)
	}
	return c.JSON(fiber.Map{"ids":ids})
}
func (h *ConclusionsHandler) Query(c *fiber.Ctx) error {
	var req struct{ WorkspaceID string `json:"workspace_id"`; Query string `json:"query"`; N int `json:"n_results"`}
	_ = c.BodyParser(&req); if req.N==0 { req.N=5 }
	results,_:=h.store.QueryConclusions(req.WorkspaceID, req.Query, req.N)
	return c.JSON(fiber.Map{"results":results})
}
