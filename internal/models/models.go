package models

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var resourceNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func ValidateResourceName(s string) bool {
	return len(s) >= 1 && len(s) <= 512 && resourceNameRe.MatchString(s)
}
func SanitizeString(s string) string { return strings.ReplaceAll(s, "\x00", "") }
func ValidateMetadata(m map[string]interface{}) error {
	if len(m) > 100 { return &validationError{"metadata exceeds 100 keys"} }
	return checkDepth(m, 1)
}
func checkDepth(m map[string]interface{}, d int) error {
	if d > 5 { return &validationError{"metadata depth >5"} }
	for _, v := range m { if mm, ok := v.(map[string]interface{}); ok { if err := checkDepth(mm, d+1); err != nil { return err } } }
	return nil
}
type validationError struct{ msg string }
func (e *validationError) Error() string { return e.msg }

// Workspace represents a top-level namespace for agent memory isolation.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func NewWorkspace(name string) *Workspace {
	return &Workspace{
		ID:        uuid.New().String(),
		Name:      name,
		CreatedAt: time.Now().UTC(),
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
		CreatedAt:   time.Now().UTC(),
	}
}

// Message represents a single message within a session.
type Message struct {
	ID          string                 `json:"id"`
	WorkspaceID string                 `json:"workspace_id"`
	SessionID   string                 `json:"session_id"`
	Role        string                 `json:"role"`
	Content     string                 `json:"content"`
	CreatedAt   time.Time              `json:"created_at"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

func NewMessage(workspaceID, sessionID, role, content string) *Message {
	return &Message{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		Role:        role,
		Content:     content,
		CreatedAt:   time.Now().UTC(),
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

type Peer struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	WorkspaceID string                 `json:"workspace_id"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
}

type PeerCard struct {
	PeerID    string   `json:"peer_id"`
	PeerName  string   `json:"peer_name"`
	Lines     []string `json:"lines"`
	UpdatedAt time.Time `json:"updated_at"`
}
