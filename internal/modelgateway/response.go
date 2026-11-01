package modelgateway

import (
	"time"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

type FinishReason string

const (
	FinishReasonStop     FinishReason = "stop"
	FinishReasonLength   FinishReason = "length"
	FinishReasonToolCall FinishReason = "tool_call"
	FinishReasonContent  FinishReason = "content_filter"
	FinishReasonUnknown  FinishReason = "unknown"
)

type GenerateResponse struct {
	ID string `json:"id"`

	ProviderID provider.ID `json:"provider_id"`
	ModelID    string      `json:"model_id"`
	Model      string      `json:"model"`

	Content      string       `json:"content"`
	ToolCalls    []ToolCall   `json:"tool_calls,omitempty"`
	FinishReason FinishReason `json:"finish_reason"`

	Usage Usage `json:"usage"`

	CreatedAt time.Time `json:"created_at"`
}

func (r *GenerateResponse) Normalize() {
	if r == nil {
		return
	}

	r.Usage.Normalize()

	if r.FinishReason == "" {
		r.FinishReason = FinishReasonUnknown
	}

	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
}
