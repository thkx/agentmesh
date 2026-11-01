package provider

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidModelID            = errors.New("invalid model id")
	ErrInvalidModelName          = errors.New("invalid model name")
	ErrInvalidModelProvider      = errors.New("invalid model provider")
	ErrModelAlreadyExists        = errors.New("model already registered")
	ErrInvalidModelContext       = errors.New("invalid model context window")
	ErrInvalidModelMaxTokens     = errors.New("invalid model max output tokens")
	ErrModelDisabled             = errors.New("model is disabled")
	ErrModelStreamingUnsupported = errors.New("model does not support streaming")
)

type Model struct {
	ID         string `json:"id"`
	ProviderID ID     `json:"provider_id"`

	// Name is the actual model identifier used by the provider API.
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`

	ContextWindow   int `json:"context_window,omitempty"`
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`

	SupportsStreaming bool `json:"supports_streaming"`

	Enabled bool `json:"enabled"`

	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func (m Model) Validate() error {
	if strings.TrimSpace(m.ID) == "" {
		return ErrInvalidModelID
	}

	if strings.TrimSpace(m.Name) == "" {
		return ErrInvalidModelName
	}

	if strings.TrimSpace(string(m.ProviderID)) == "" {
		return ErrInvalidModelProvider
	}

	if m.ContextWindow < 0 {
		return ErrInvalidModelContext
	}

	if m.MaxOutputTokens < 0 {
		return ErrInvalidModelMaxTokens
	}

	if m.ContextWindow > 0 &&
		m.MaxOutputTokens > m.ContextWindow {
		return fmt.Errorf(
			"%w: max output tokens exceed context window",
			ErrInvalidModelMaxTokens,
		)
	}

	return nil
}

func (m Model) IsEnabled() bool {
	return m.Enabled
}

func (m Model) CanStream() bool {
	return m.SupportsStreaming
}
