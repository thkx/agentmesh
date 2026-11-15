package execution

import (
	"errors"
	"strings"
	"time"
)

type Provider string

const (
	ProviderClaude   Provider = "claude"
	ProviderCodex    Provider = "codex"
	ProviderGemini   Provider = "gemini"
	ProviderOpenCode Provider = "opencode"
)

type PermissionMode string

const (
	PermissionReadOnly       PermissionMode = "read-only"
	PermissionWorkspaceWrite PermissionMode = "workspace-write"
)

type Status string

const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
	StatusTimedOut  Status = "timed_out"
)

var (
	ErrInvalidRequest      = errors.New("invalid execution request")
	ErrUnsupportedProvider = errors.New("unsupported coding agent provider")
	ErrExecutionNotFound   = errors.New("execution not found")
)

type Request struct {
	ID             string         `json:"id"`
	Provider       Provider       `json:"provider"`
	Prompt         string         `json:"prompt"`
	WorkspacePath  string         `json:"workspace_path"`
	Model          string         `json:"model,omitempty"`
	PermissionMode PermissionMode `json:"permission_mode,omitempty"`
	Timeout        time.Duration  `json:"timeout,omitempty"`
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.Prompt) == "" || strings.TrimSpace(r.WorkspacePath) == "" {
		return ErrInvalidRequest
	}
	switch r.Provider {
	case ProviderClaude, ProviderCodex, ProviderGemini, ProviderOpenCode:
	default:
		return ErrUnsupportedProvider
	}
	if r.PermissionMode != "" && r.PermissionMode != PermissionReadOnly && r.PermissionMode != PermissionWorkspaceWrite {
		return ErrInvalidRequest
	}
	if r.Timeout < 0 {
		return ErrInvalidRequest
	}
	return nil
}

type Result struct {
	ID         string        `json:"id"`
	Provider   Provider      `json:"provider"`
	Status     Status        `json:"status"`
	Output     string        `json:"output,omitempty"`
	SessionID  string        `json:"session_id,omitempty"`
	Stderr     string        `json:"stderr,omitempty"`
	ExitCode   int           `json:"exit_code"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	Duration   time.Duration `json:"duration"`
}
