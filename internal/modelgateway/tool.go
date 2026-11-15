package modelgateway

import (
	"encoding/json"
	"errors"
	"strings"
)

var ErrInvalidToolCall = errors.New("invalid model tool call")

// ToolDefinition is the provider-neutral function schema exposed to a model.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall is a normalized native function call returned by a model provider.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (t ToolCall) Validate() error {
	if strings.TrimSpace(t.ID) == "" || strings.TrimSpace(t.Name) == "" || len(t.Arguments) == 0 || len(t.Arguments) > 1<<20 || !json.Valid(t.Arguments) {
		return ErrInvalidToolCall
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(t.Arguments, &object); err != nil || object == nil {
		return ErrInvalidToolCall
	}
	return nil
}
