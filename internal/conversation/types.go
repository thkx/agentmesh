package conversation

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalid  = errors.New("invalid conversation request")
	ErrNotFound = errors.New("conversation not found")
)

type Scope string

const (
	ScopeGlobal   Scope = "global"
	ScopeAgent    Scope = "agent"
	ScopeGoal     Scope = "goal"
	ScopeWorkflow Scope = "workflow"
)

type Conversation struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	ScopeType  Scope          `json:"scope_type"`
	ScopeID    string         `json:"scope_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	ArchivedAt time.Time      `json:"archived_at,omitempty"`
}

type Message struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversation_id"`
	Sequence       int64          `json:"sequence"`
	Role           string         `json:"role"`
	Name           string         `json:"name,omitempty"`
	Content        string         `json:"content"`
	ToolCallsJSON  string         `json:"tool_calls_json,omitempty"`
	ToolCallID     string         `json:"tool_call_id,omitempty"`
	TokenEstimate  int            `json:"token_estimate"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

func (c Conversation) Validate() error {
	if strings.TrimSpace(c.ID) == "" || len(c.ID) > 128 || len(c.Title) > 200 {
		return ErrInvalid
	}
	switch c.ScopeType {
	case ScopeGlobal, ScopeAgent, ScopeGoal, ScopeWorkflow:
	default:
		return ErrInvalid
	}
	if (c.ScopeType == ScopeGlobal && strings.TrimSpace(c.ScopeID) != "") || (c.ScopeType != ScopeGlobal && strings.TrimSpace(c.ScopeID) == "") {
		return ErrInvalid
	}
	if len(c.ScopeID) > 256 {
		return ErrInvalid
	}
	return nil
}
func (m Message) Validate() error {
	if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.ConversationID) == "" || len(m.Content) > 1<<20 {
		return ErrInvalid
	}
	switch m.Role {
	case "system", "developer", "user", "assistant", "tool":
	default:
		return ErrInvalid
	}
	if m.Role == "tool" && m.ToolCallID == "" {
		return ErrInvalid
	}
	if m.ToolCallsJSON != "" {
		if m.Role != "assistant" || !json.Valid([]byte(m.ToolCallsJSON)) {
			return ErrInvalid
		}
	}
	return nil
}
