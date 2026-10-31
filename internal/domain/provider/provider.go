package provider

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type ID string

const (
	IDOpenAI    ID = "openai"
	IDAnthropic ID = "anthropic"
	IDGemini    ID = "gemini"
	IDOllama    ID = "ollama"
)

type Kind string

const (
	KindCloud Kind = "cloud"
	KindLocal Kind = "local"
)

var (
	ErrInvalidProviderID   = errors.New("invalid provider id")
	ErrInvalidProviderName = errors.New("invalid provider name")
	ErrInvalidProviderKind = errors.New("invalid provider kind")
)

type Provider struct {
	ID      ID     `json:"id"`
	Name    string `json:"name"`
	Kind    Kind   `json:"kind"`
	BaseURL string `json:"base_url,omitempty"`

	Enabled bool `json:"enabled"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (p Provider) Validate() error {
	if strings.TrimSpace(string(p.ID)) == "" {
		return ErrInvalidProviderID
	}

	if strings.TrimSpace(p.Name) == "" {
		return ErrInvalidProviderName
	}

	switch p.Kind {
	case KindCloud, KindLocal:
	default:
		return fmt.Errorf("%w: %q", ErrInvalidProviderKind, p.Kind)
	}

	return nil
}

func (p Provider) IsEnabled() bool {
	return p.Enabled
}
