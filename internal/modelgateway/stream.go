package modelgateway

import (
	"time"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

type StreamEventType string

const (
	StreamEventTextDelta StreamEventType = "text_delta"
	StreamEventUsage     StreamEventType = "usage"
	StreamEventCompleted StreamEventType = "completed"
	StreamEventError     StreamEventType = "error"
)

type StreamEvent struct {
	Type StreamEventType `json:"type"`

	ProviderID provider.ID `json:"provider_id,omitempty"`
	ModelID    string      `json:"model_id,omitempty"`

	Delta string `json:"delta,omitempty"`

	Usage    *Usage            `json:"usage,omitempty"`
	Response *GenerateResponse `json:"response,omitempty"`

	Error error `json:"-"`

	CreatedAt time.Time `json:"created_at"`
}

func NewTextDeltaEvent(providerID provider.ID, modelID string, delta string) StreamEvent {
	return StreamEvent{
		Type:       StreamEventTextDelta,
		ProviderID: providerID,
		ModelID:    modelID,
		Delta:      delta,
		CreatedAt:  time.Now().UTC(),
	}
}

func NewUsageEvent(providerID provider.ID, modelID string, usage Usage) StreamEvent {
	usage.Normalize()

	return StreamEvent{
		Type:       StreamEventUsage,
		ProviderID: providerID,
		ModelID:    modelID,
		Usage:      &usage,
		CreatedAt:  time.Now().UTC(),
	}
}

func NewCompletedEvent(response *GenerateResponse) StreamEvent {
	if response != nil {
		response.Normalize()
	}

	return StreamEvent{
		Type:      StreamEventCompleted,
		Response:  response,
		CreatedAt: time.Now().UTC(),
	}
}

func NewErrorEvent(err error) StreamEvent {
	return StreamEvent{
		Type:      StreamEventError,
		Error:     err,
		CreatedAt: time.Now().UTC(),
	}
}
