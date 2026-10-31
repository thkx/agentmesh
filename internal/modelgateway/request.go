package modelgateway

import (
	"errors"
	"fmt"
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
	Role    Role   `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
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

	Metadata map[string]string `json:"metadata,omitempty"`
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

		if strings.TrimSpace(message.Content) == "" {
			return fmt.Errorf(
				"%w at messages[%d]",
				ErrEmptyMessage,
				i,
			)
		}
	}

	if r.Temperature != nil {
		if *r.Temperature < 0 || *r.Temperature > 2 {
			return ErrInvalidTemperature
		}
	}

	if r.TopP != nil {
		if *r.TopP < 0 || *r.TopP > 1 {
			return ErrInvalidTopP
		}
	}

	if r.MaxOutputTokens < 0 {
		return ErrInvalidMaxTokens
	}

	return nil
}
