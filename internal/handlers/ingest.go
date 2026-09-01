package handlers

import (
	"io"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/models"
	"github.com/alfirus/vectorizer/internal/store"
)

type IngestHandler struct{ store *store.Store }
func NewIngestHandler(s *store.Store) *IngestHandler { return &IngestHandler{store: s} }

func (h *IngestHandler) Upload(c *fiber.Ctx) error {
	ws:=c.FormValue("workspace_id")
	sess:=c.FormValue("session_id")
	content:=""
	// JSON fallback: MCP sends {workspace_id, session_id, content} as JSON
	if ws=="" || sess=="" {
		var j struct{
			WorkspaceID string `json:"workspace_id"`
			SessionID string `json:"session_id"`
			Content string `json:"content"`
			Text string `json:"text"`
			Body string `json:"body"`
			File string `json:"file"`
		}
		_ = c.BodyParser(&j)
		if j.WorkspaceID!="" { ws=j.WorkspaceID }
		if j.SessionID!="" { sess=j.SessionID }
		if j.Content!="" { content=j.Content } else if j.Text!="" { content=j.Text } else if j.Body!="" { content=j.Body } else if j.File!="" { content=j.File }
	}
	if ws=="" || sess=="" { return c.Status(400).JSON(fiber.Map{"error":"workspace_id and session_id required"})}
	// if content already extracted from JSON, ingest directly
	if content!="" {
		content=strings.ReplaceAll(content, "\x00", "")
		return h.ingestText(ws, sess, content)
	}
	fh, err:=c.FormFile("file")
	if err!=nil {
		// fallback raw body or JSON body-as-text
		body:=string(c.Body())
		if body=="" { return c.Status(400).JSON(fiber.Map{"error":"file or content required"})}
		// if body was JSON we already parsed content; if still empty treat raw body as text
		// avoid double-JSON: if body looks like JSON with content field, prefer parsed content
		if content=="" { content=body }
		content=strings.ReplaceAll(content, "\x00", "")
		return h.ingestText(ws, sess, content)
	}
	f, _:=fh.Open(); defer f.Close()
	b,_:=io.ReadAll(f)
	text:=string(b)
	// light parse: if PDF etc, just treat as text
	text=strings.ReplaceAll(text, "\x00", "")
	return h.ingestText(ws, sess, text)
}

func (h *IngestHandler) ingestText(ws, sess, text string) error {
	msg:=models.NewMessage(ws, sess, "system", text)
	// store handles chunking + 1536d (Qwen3)
	if err:=h.store.AddMessage(msg, text); err!=nil { return fiber.NewError(500, "ingest failed") }
	return nil
}

func (h *IngestHandler) Grep(c *fiber.Ctx) error {
	ws:=c.Query("workspace_id"); q:=c.Query("q")
	if ws==""||q=="" { return c.Status(400).JSON(fiber.Map{"error":"workspace_id and q required"})}
	docs,_:=h.store.Grep(ws, c.Query("session_id"), q, 50)
	return c.JSON(fiber.Map{"results":docs})
}

func (h *IngestHandler) Temporal(c *fiber.Ctx) error {
	ws:=c.Query("workspace_id"); q:=c.Query("q")
	after:=c.Query("after"); before:=c.Query("before")
	docs,_,_:=h.store.SearchWithOptions(q, ws, c.Query("session_id"), "", 10, 0, after, before)
	return c.JSON(fiber.Map{"results":docs})
}
