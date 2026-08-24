package models

import (
	"time"

	"github.com/google/uuid"
)

// Workspace represents a top-level namespace for agent memory isolation.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func NewWorkspace(name string) *Workspace {
	return &Workspace{
		ID:    uuid.New().String(),
		Name:  name,
	}
}

// Session represents a conversation or interaction within a workspace.
type Session struct {
	ID        string    `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Title     string    `json:"title,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func NewSession(workspaceID, title string) *Session {
	return &Session{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		Title:       title,
	}
}

// Message represents a single message within a session.
type Message struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	SessionID   string    `json:"session_id"`
	Role        string    `json:"role"` // "user", "assistant", "system"
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
}

func NewMessage(workspaceID, sessionID, role, content string) *Message {
	return &Message{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		Role:        role,
		Content:     content,
	}
}

// SearchRequest represents a semantic search query.
type SearchRequest struct {
	Query    string                 `json:"query"`
	NResults int                    `json:"n_results,omitempty"` // default 10
	Where    map[string]interface{} `json:"where"`               // metadata filters: workspace_id, session_id, role, etc.
}

// SearchResult represents a single search result with the matched document and metadata.
type SearchResult struct {
	ID       string                 `json:"id"`
	Document string                 `json:"document,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Distance float32                `json:"distance,omitempty"`
}
