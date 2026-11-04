package tool

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidSpec      = errors.New("invalid tool specification")
	ErrInvalidArguments = errors.New("invalid tool arguments")
	ErrNotFound         = errors.New("tool not found")
	ErrDenied           = errors.New("tool invocation denied")
	ErrApprovalRequired = errors.New("tool invocation requires approval")
	ErrOutputTooLarge   = errors.New("tool output exceeds limit")
)

type Permission string

const (
	PermissionRead    Permission = "read"
	PermissionWrite   Permission = "write"
	PermissionExecute Permission = "execute"
)

type Property struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}
type Schema struct {
	Type                 string              `json:"type"`
	Properties           map[string]Property `json:"properties"`
	Required             []string            `json:"required,omitempty"`
	AdditionalProperties bool                `json:"additionalProperties"`
}
type Spec struct {
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	Input            Schema        `json:"input_schema"`
	Permission       Permission    `json:"permission"`
	RequiresApproval bool          `json:"requires_approval"`
	Timeout          time.Duration `json:"-"`
}

func (s Spec) Validate() error {
	if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Description) == "" || s.Input.Type != "object" {
		return ErrInvalidSpec
	}
	switch s.Permission {
	case PermissionRead, PermissionWrite, PermissionExecute:
	default:
		return ErrInvalidSpec
	}
	for name, p := range s.Input.Properties {
		if strings.TrimSpace(name) == "" {
			return ErrInvalidSpec
		}
		switch p.Type {
		case "string", "integer", "number", "boolean", "array", "object":
		default:
			return ErrInvalidSpec
		}
	}
	for _, r := range s.Input.Required {
		if _, ok := s.Input.Properties[r]; !ok {
			return ErrInvalidSpec
		}
	}
	if s.Timeout < 0 {
		return ErrInvalidSpec
	}
	return nil
}
