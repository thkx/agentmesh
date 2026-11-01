package modelgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type GenerateRequest struct {
	// ModelID is the internal AgentMesh model identifier.
	ModelID string `json:"model_id"`

	// The following two fields are populated by Gateway
	// after resolving ModelID. Adapters should use ProviderModel.
	ProviderID    provider.ID `json:"-"`
	ProviderModel string      `json:"-"`

	System   string    `json:"system,omitempty"`
	Messages []Message `json:"messages"`

	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`

	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
	StopSequences   []string `json:"stop_sequences,omitempty"`

	Metadata   map[string]string `json:"metadata,omitempty"`
	Tools      []ToolDefinition  `json:"tools,omitempty"`
	ToolChoice string            `json:"tool_choice,omitempty"`
}

var (
	ErrMissingModelID     = errors.New("model id is required")
	ErrMissingMessages    = errors.New("at least one message is required")
	ErrInvalidMessageRole = errors.New("invalid message role")
	ErrEmptyMessage       = errors.New("message content is empty")
	ErrInvalidTemperature = errors.New("temperature must be between 0 and 2")
	ErrInvalidTopP        = errors.New("top_p must be between 0 and 1")
	ErrInvalidMaxTokens   = errors.New("max output tokens must not be negative")
)

func (r GenerateRequest) Validate() error {
	if strings.TrimSpace(r.ModelID) == "" {
		return ErrMissingModelID
	}

	if len(r.Messages) == 0 {
		return ErrMissingMessages
	}

	for i, message := range r.Messages {
		switch message.Role {
		case RoleSystem, RoleDeveloper, RoleUser, RoleAssistant, RoleTool:
		default:
			return fmt.Errorf(
				"%w at messages[%d]: %q",
				ErrInvalidMessageRole,
				i,
				message.Role,
			)
		}

		if strings.TrimSpace(message.Content) == "" && !(message.Role == RoleAssistant && len(message.ToolCalls) > 0) {
			return fmt.Errorf(
				"%w at messages[%d]",
				ErrEmptyMessage,
				i,
			)
		}
		if len(message.ToolCalls) > 0 {
			if message.Role != RoleAssistant {
				return fmt.Errorf("tool calls are only valid on assistant messages at messages[%d]", i)
			}
			for _, call := range message.ToolCalls {
				if err := call.Validate(); err != nil {
					return fmt.Errorf("invalid tool call at messages[%d]: %w", i, err)
				}
			}
		}
		if message.Role == RoleTool && strings.TrimSpace(message.ToolCallID) == "" {
			return fmt.Errorf("tool call id is required at messages[%d]", i)
		}
	}

	if r.Temperature != nil {
		if math.IsNaN(*r.Temperature) || math.IsInf(*r.Temperature, 0) || *r.Temperature < 0 || *r.Temperature > 2 {
			return ErrInvalidTemperature
		}
	}

	if r.TopP != nil {
		if math.IsNaN(*r.TopP) || math.IsInf(*r.TopP, 0) || *r.TopP < 0 || *r.TopP > 1 {
			return ErrInvalidTopP
		}
	}

	if len(r.Tools) > 128 {
		return errors.New("too many tool definitions")
	}
	seenTools := make(map[string]struct{}, len(r.Tools))
	for _, tool := range r.Tools {
		if strings.TrimSpace(tool.Name) == "" || len(tool.Parameters) == 0 || !json.Valid(tool.Parameters) {
			return errors.New("invalid tool definition")
		}
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil || schema == nil || string(schema["type"]) != `"object"` {
			return errors.New("tool parameters must be an object JSON Schema")
		}
		if _, ok := seenTools[tool.Name]; ok {
			return errors.New("duplicate tool definition")
		}
		seenTools[tool.Name] = struct{}{}
	}
	if r.MaxOutputTokens < 0 {
		return ErrInvalidMaxTokens
	}

	return nil
}
