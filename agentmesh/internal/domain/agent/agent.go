package agent

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidTask        = errors.New("invalid agent task")
	ErrTaskNotFound       = errors.New("agent task not found")
	ErrMaxSteps           = errors.New("agent reached maximum steps")
	ErrMaxToolCalls       = errors.New("agent reached maximum tool calls")
	ErrRepeatedToolCall   = errors.New("agent repeated the same tool call")
	ErrInvalidModelOutput = errors.New("agent returned invalid control output")
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
	StatusTimedOut  Status = "timed_out"
)

type Task struct {
	ID              string   `json:"id"`
	ModelID         string   `json:"model_id"`
	WorkspaceID     string   `json:"workspace_id"`
	Prompt          string   `json:"prompt"`
	MaxSteps        int      `json:"max_steps,omitempty"`
	MaxToolCalls    int      `json:"max_tool_calls,omitempty"`
	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
	ApprovedTools   []string `json:"approved_tools,omitempty"`
}

func (t Task) Validate() error {
	if strings.TrimSpace(t.ID) == "" || strings.TrimSpace(t.ModelID) == "" || strings.TrimSpace(t.WorkspaceID) == "" || strings.TrimSpace(t.Prompt) == "" {
		return ErrInvalidTask
	}
	if t.MaxSteps < 0 || t.MaxToolCalls < 0 || t.MaxOutputTokens < 0 || t.TimeoutSeconds < 0 || t.TimeoutSeconds > 3600 {
		return ErrInvalidTask
	}
	if len(t.ApprovedTools) > 64 {
		return ErrInvalidTask
	}
	for _, name := range t.ApprovedTools {
		if strings.TrimSpace(name) == "" {
			return ErrInvalidTask
		}
	}
	return nil
}

type Result struct {
	TaskID     string    `json:"task_id"`
	Status     Status    `json:"status"`
	Content    string    `json:"content,omitempty"`
	Steps      int       `json:"steps"`
	ToolCalls  int       `json:"tool_calls"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Error      string    `json:"error,omitempty"`
}
