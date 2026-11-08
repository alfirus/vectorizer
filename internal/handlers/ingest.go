package handlers

import (
	"io"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/alfirus/vectorizer/internal/models"
	"github.com/alfirus/vectorizer/internal/store"
)

// maxUploadBytes caps a single upload read (multipart file or raw/JSON
// body). Every other write path enforces a 100k-char cap; upload must not
// be the unbounded one (F7).
const maxUploadBytes = 100000

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
	if !requireWorkspace(c, ws) {
		return nil
	}
	// if content already extracted from JSON, ingest directly
	if content!="" {
		content=strings.ReplaceAll(content, "\x00", "")
		return h.ingestText(c, ws, sess, content)
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
		return h.ingestText(c, ws, sess, content)
	}
	// F7: Open can fail (nil file -> Close panic). Cap the read so a
	// multi-GB upload is not buffered into RAM.
	f, err:=fh.Open()
	if err!=nil {
		return c.Status(400).JSON(fiber.Map{"error":"cannot open uploaded file"})
	}
	defer f.Close()
	b,err:=io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err!=nil {
		return c.Status(400).JSON(fiber.Map{"error":"cannot read uploaded file"})
	}
	if len(b) > maxUploadBytes {
		return c.Status(413).JSON(fiber.Map{"error":"upload too large (max 100k chars)"})
	}
	text:=string(b)
	// light parse: if PDF etc, just treat as text
	text=strings.ReplaceAll(text, "\x00", "")
	return h.ingestText(c, ws, sess, text)
}

func (h *IngestHandler) ingestText(c *fiber.Ctx, ws, sess, text string) error {
	if len(text) > maxUploadBytes {
		return c.Status(413).JSON(fiber.Map{"error":"content too large (max 100k chars)"})
	}
	msg:=models.NewMessage(ws, sess, "system", text)
	// store handles chunking + embedding
	if err:=h.store.AddMessage(msg, text); err!=nil { return fiber.NewError(500, "ingest failed") }
	return c.Status(201).JSON(fiber.Map{"id": msg.ID, "workspace_id": ws, "session_id": sess, "stored": true})
}

func (h *IngestHandler) Grep(c *fiber.Ctx) error {
	ws:=c.Query("workspace_id"); q:=c.Query("q")
	if ws==""||q=="" { return c.Status(400).JSON(fiber.Map{"error":"workspace_id and q required"})}
	if !requireWorkspace(c, ws) {
		return nil
	}
	docs,_:=h.store.Grep(ws, c.Query("session_id"), q, 50)
	return c.JSON(fiber.Map{"results":docs})
}

func (h *IngestHandler) Temporal(c *fiber.Ctx) error {
	ws:=c.Query("workspace_id"); q:=c.Query("q")
	if !requireWorkspace(c, ws) {
		return nil
	}
	after:=c.Query("after"); before:=c.Query("before")
	docs,_,_:=h.store.SearchWithOptions(q, ws, c.Query("session_id"), "", 10, 0, after, before)
	return c.JSON(fiber.Map{"results":docs})
}
