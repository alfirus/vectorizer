package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/store"
)

type PeersHandler struct{ store *store.Store }
func NewPeersHandler(s *store.Store) *PeersHandler { return &PeersHandler{store: s} }

func (h *PeersHandler) CreatePeer(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id")
	var req struct{ ID string `json:"id"`; Name string `json:"name"`; Metadata map[string]interface{} `json:"metadata"`}
	if err:=c.BodyParser(&req); err!=nil { return c.Status(400).JSON(fiber.Map{"error":"invalid body"})}
	name:=req.Name; if name=="" { name=req.ID }
	if name=="" { return c.Status(400).JSON(fiber.Map{"error":"peer id required"})}
	id, err:=h.store.CreatePeer(ws, name, req.Metadata)
	if err!=nil { return c.Status(500).JSON(fiber.Map{"error":"failed to create peer"})}
	return c.Status(201).JSON(fiber.Map{"id":id, "name":name})
}
func (h *PeersHandler) ListPeers(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id")
	peers,_:=h.store.ListPeers(ws)
	return c.JSON(fiber.Map{"peers":peers})
}
func (h *PeersHandler) SetPeerCard(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id"); pid:=c.Params("peer_id")
	var req struct{ Lines []string `json:"lines"`}
	if err:=c.BodyParser(&req); err!=nil { return c.Status(400).JSON(fiber.Map{"error":"invalid body"})}
	if err:=h.store.SetPeerCard(ws, pid, req.Lines); err!=nil { return c.Status(500).JSON(fiber.Map{"error":"failed to set peer card"})}
	return c.JSON(fiber.Map{"updated":true})
}
func (h *PeersHandler) GetPeerCard(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id"); pid:=c.Params("peer_id")
	lines, _:=h.store.GetPeerCard(ws, pid)
	return c.JSON(fiber.Map{"peer_id":pid, "lines":lines})
}
func (h *PeersHandler) UpdatePeer(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"updated":true})
}
func (h *PeersHandler) Sessions(c *fiber.Ctx) error {
	ws:=c.Params("workspace_id")
	sess,_:=h.store.ListSessions(ws)
	return c.JSON(fiber.Map{"sessions":sess})
}
