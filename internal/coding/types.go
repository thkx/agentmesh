package coding

import (
	"errors"
	"strings"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusTesting   Status = "testing"
	StatusRepairing Status = "repairing"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
	// StatusInterrupted means the process stopped before the runtime outcome was known.
	StatusInterrupted Status = "interrupted"
)

var (
	ErrNotFound      = errors.New("coding task not found")
	ErrInvalidTask   = errors.New("invalid coding task")
	ErrConflict      = errors.New("coding task state conflict")
	ErrUnsafeCommand = errors.New("test command is not allowed")
)

type Command struct {
	Program string   `json:"program"`
	Args    []string `json:"args,omitempty"`
}
type Task struct {
	ID                string    `json:"id"`
	ExecutionID       string    `json:"execution_id,omitempty"`
	OutcomeUnknown    bool      `json:"outcome_unknown,omitempty"`
	Title             string    `json:"title"`
	RepositoryPath    string    `json:"repository_path"`
	ModelID           string    `json:"model_id"`
	Prompt            string    `json:"prompt"`
	Status            Status    `json:"status"`
	WorkspaceID       string    `json:"workspace_id"`
	WorkspacePath     string    `json:"workspace_path"`
	BranchName        string    `json:"branch_name"`
	TestCommand       Command   `json:"test_command"`
	MaxRepairAttempts int       `json:"max_repair_attempts"`
	RepairAttempts    int       `json:"repair_attempts"`
	TimeoutSeconds    int       `json:"timeout_seconds"`
	Result            string    `json:"result,omitempty"`
	Error             string    `json:"error,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	FinishedAt        time.Time `json:"finished_at,omitempty"`
}
type CreateRequest struct {
	ID                string  `json:"id"`
	ExecutionID       string  `json:"execution_id,omitempty"`
	Title             string  `json:"title"`
	RepositoryPath    string  `json:"repository_path"`
	ModelID           string  `json:"model_id"`
	Prompt            string  `json:"prompt"`
	TestCommand       Command `json:"test_command"`
	MaxRepairAttempts int     `json:"max_repair_attempts"`
	TimeoutSeconds    int     `json:"timeout_seconds"`
}

func (r CreateRequest) Validate() error {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.RepositoryPath) == "" || strings.TrimSpace(r.ModelID) == "" || strings.TrimSpace(r.Prompt) == "" {
		return ErrInvalidTask
	}
	if len(r.ID) > 80 || r.ID == "." || r.ID == ".." {
		return ErrInvalidTask
	}
	for _, ch := range r.ID {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return ErrInvalidTask
		}
	}
	if r.MaxRepairAttempts < 0 || r.MaxRepairAttempts > 5 || r.TimeoutSeconds < 0 || r.TimeoutSeconds > 7200 {
		return ErrInvalidTask
	}
	if r.TestCommand.Program != "" {
		if err := ValidateCommand(r.TestCommand); err != nil {
			return err
		}
	}
	return nil
}
func ValidateCommand(c Command) error {
	allowed := map[string]bool{"go": true, "npm": true, "pnpm": true, "yarn": true, "pytest": true, "python": true, "cargo": true, "make": true}
	if !allowed[c.Program] || len(c.Args) > 32 {
		return ErrUnsafeCommand
	}
	for _, a := range c.Args {
		if strings.ContainsAny(a, ";&|`$><\n\r") {
			return ErrUnsafeCommand
		}
	}
	return nil
}

type Step struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"task_id"`
	Sequence   int       `json:"sequence"`
	Kind       string    `json:"kind"`
	Status     string    `json:"status"`
	Input      string    `json:"input,omitempty"`
	Output     string    `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}
type Event struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Type      string    `json:"type"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}
type Snapshot struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"task_id"`
	Label      string    `json:"label"`
	CommitHash string    `json:"commit_hash"`
	Diff       string    `json:"diff"`
	CreatedAt  time.Time `json:"created_at"`
}
