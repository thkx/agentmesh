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

type Type string

type Kind string

const (
	TypeOpenAI    Type = "openai"
	TypeAnthropic Type = "anthropic"
	TypeGemini    Type = "gemini"
	TypeOllama    Type = "ollama"

	KindCloud Kind = "cloud"
	KindLocal Kind = "local"
)

var (
	ErrInvalidProviderID   = errors.New("invalid provider id")
	ErrInvalidProviderName = errors.New("invalid provider name")
	ErrInvalidProviderKind = errors.New("invalid provider kind")
)

type Provider struct {
	ID            ID     `json:"id"`
	Type          Type   `json:"type"`
	Name          string `json:"name"`
	Kind          Kind   `json:"kind"`
	BaseURL       string `json:"base_url,omitempty"`
	CredentialRef string `json:"-"`

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

	switch p.Type {
	case TypeOpenAI, TypeAnthropic, TypeGemini, TypeOllama:
	default:
		return fmt.Errorf("unsupported provider type: %q", p.Type)
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
