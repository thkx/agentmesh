package agent

import (
	"errors"
	"strings"
	"time"
)

var ErrInvalidDefinition = errors.New("invalid agent definition")

type Definition struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	SystemPrompt    string    `json:"system_prompt"`
	ModelID         string    `json:"model_id"`
	Temperature     float64   `json:"temperature"`
	MaxSteps        int       `json:"max_steps"`
	MaxToolCalls    int       `json:"max_tool_calls"`
	MaxOutputTokens int       `json:"max_output_tokens"`
	TimeoutSeconds  int       `json:"timeout_seconds"`
	ApprovedTools   []string  `json:"approved_tools,omitempty"`
	Enabled         bool      `json:"enabled"`
	Version         int       `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (d Definition) Validate() error {
	if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.SystemPrompt) == "" || strings.TrimSpace(d.ModelID) == "" {
		return ErrInvalidDefinition
	}
	if len(d.Name) > 160 || len(d.Description) > 2000 || len(d.SystemPrompt) > 64<<10 {
		return ErrInvalidDefinition
	}
	if d.Temperature < 0 || d.Temperature > 2 || d.MaxSteps < 0 || d.MaxToolCalls < 0 || d.MaxOutputTokens < 0 || d.TimeoutSeconds < 0 || d.TimeoutSeconds > 3600 {
		return ErrInvalidDefinition
	}
	if len(d.ApprovedTools) > 64 {
		return ErrInvalidDefinition
	}
	seen := map[string]struct{}{}
	for _, name := range d.ApprovedTools {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 128 {
			return ErrInvalidDefinition
		}
		if _, ok := seen[name]; ok {
			return ErrInvalidDefinition
		}
		seen[name] = struct{}{}
	}
	return nil
}
