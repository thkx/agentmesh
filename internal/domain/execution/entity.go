package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Kind string
type Runtime string
type LifecycleStatus string
type AttemptStatus string

const (
	KindAgent Kind = "agent"
	KindCoding Kind = "coding"
	KindWorkflow Kind = "workflow"
	KindCLI Kind = "cli"

	RuntimeAgent Runtime = "agent"
	RuntimeCoding Runtime = "coding"
	RuntimeWorkflow Runtime = "workflow"
	RuntimeCLI Runtime = "cli"

	StatusQueued LifecycleStatus = "queued"
	StatusRunning LifecycleStatus = "running"
	StatusSucceeded LifecycleStatus = "succeeded"
	StatusFailed LifecycleStatus = "failed"
	StatusCanceled LifecycleStatus = "canceled"
	StatusTimedOut LifecycleStatus = "timed_out"
	StatusInterrupted LifecycleStatus = "interrupted"
	StatusNeedsReconciliation LifecycleStatus = "needs_reconciliation"

	AttemptRunning AttemptStatus = "running"
	AttemptSucceeded AttemptStatus = "succeeded"
	AttemptFailed AttemptStatus = "failed"
	AttemptCanceled AttemptStatus = "canceled"
	AttemptTimedOut AttemptStatus = "timed_out"
	AttemptUnknown AttemptStatus = "unknown"
)

var (
	ErrInvalidEntity = errors.New("invalid execution entity")
	ErrInvalidAttempt = errors.New("invalid execution attempt")
	ErrInvalidTransition = errors.New("invalid execution status transition")
	ErrNotFound = errors.New("execution not found")
	ErrConflict = errors.New("execution conflict")
)

type Entity struct {
	ID string `json:"id"`
	Kind Kind `json:"kind"`
	Runtime Runtime `json:"runtime"`
	RuntimeRef string `json:"runtime_ref,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	Status LifecycleStatus `json:"status"`
	Input json.RawMessage `json:"input,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error string `json:"error,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type Attempt struct {
	ID string `json:"id"`
	ExecutionID string `json:"execution_id"`
	Number int `json:"number"`
	Runtime Runtime `json:"runtime"`
	RuntimeRef string `json:"runtime_ref,omitempty"`
	Status AttemptStatus `json:"status"`
	Input json.RawMessage `json:"input,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error string `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ListFilter struct {
	Status LifecycleStatus
	Kind Kind
	Runtime Runtime
	TaskID string
	Limit int
	Offset int
}

func (e Entity) Validate() error {
	if strings.TrimSpace(e.ID)=="" || len(e.ID)>128 || !validKind(e.Kind) || !validRuntime(e.Runtime) || !validLifecycle(e.Status) {
		return ErrInvalidEntity
	}
	if len(e.Input)>1<<20 || len(e.Result)>4<<20 || len(e.Error)>4096 || len(e.IdempotencyKey)>256 || len(e.RuntimeRef)>256 || len(e.TaskID)>128 {
		return ErrInvalidEntity
	}
	if len(e.Input)>0 && !json.Valid(e.Input) { return ErrInvalidEntity }
	if len(e.Result)>0 && !json.Valid(e.Result) { return ErrInvalidEntity }
	if e.StartedAt!=nil && e.FinishedAt!=nil && e.FinishedAt.Before(*e.StartedAt) { return ErrInvalidEntity }
	return nil
}

func (a Attempt) Validate() error {
	if strings.TrimSpace(a.ID)=="" || len(a.ID)>128 || strings.TrimSpace(a.ExecutionID)=="" || a.Number<1 || !validRuntime(a.Runtime) || !validAttempt(a.Status) {
		return ErrInvalidAttempt
	}
	if len(a.Input)>1<<20 || len(a.Result)>4<<20 || len(a.Error)>4096 || len(a.RuntimeRef)>256 { return ErrInvalidAttempt }
	if len(a.Input)>0 && !json.Valid(a.Input) { return ErrInvalidAttempt }
	if len(a.Result)>0 && !json.Valid(a.Result) { return ErrInvalidAttempt }
	if a.FinishedAt!=nil && a.FinishedAt.Before(a.StartedAt) { return ErrInvalidAttempt }
	return nil
}

func CanTransition(from, to LifecycleStatus) bool {
	if from==to { return true }
	switch from {
	case StatusQueued:
		return to==StatusRunning || to==StatusCanceled || to==StatusFailed
	case StatusRunning:
		return to==StatusQueued || to==StatusSucceeded || to==StatusFailed || to==StatusCanceled || to==StatusTimedOut || to==StatusInterrupted || to==StatusNeedsReconciliation
	case StatusInterrupted, StatusNeedsReconciliation:
		return to==StatusSucceeded || to==StatusFailed || to==StatusCanceled
	default:
		return false
	}
}

func validKind(v Kind) bool { return v==KindAgent || v==KindCoding || v==KindWorkflow || v==KindCLI }
func validRuntime(v Runtime) bool { return v==RuntimeAgent || v==RuntimeCoding || v==RuntimeWorkflow || v==RuntimeCLI }
func validLifecycle(v LifecycleStatus) bool {
	switch v { case StatusQueued,StatusRunning,StatusSucceeded,StatusFailed,StatusCanceled,StatusTimedOut,StatusInterrupted,StatusNeedsReconciliation: return true }; return false
}
func validAttempt(v AttemptStatus) bool {
	switch v { case AttemptRunning,AttemptSucceeded,AttemptFailed,AttemptCanceled,AttemptTimedOut,AttemptUnknown: return true }; return false
}

func (s LifecycleStatus) String() string { return string(s) }
func (e Entity) String() string { return fmt.Sprintf("execution(%s,%s)",e.ID,e.Status) }
