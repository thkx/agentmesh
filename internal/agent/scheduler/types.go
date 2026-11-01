package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
)

var (
	ErrNotFound       = errors.New("scheduler job not found")
	ErrConflict       = errors.New("scheduler job already exists")
	ErrLeaseLost      = errors.New("scheduler job lease lost")
	ErrIndeterminate  = errors.New("tool invocation outcome is indeterminate; refusing replay")
	ErrAlreadyStarted = errors.New("scheduler job has already started")
)

type Status string

const (
	Queued      Status = "queued"
	Running     Status = "running"
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
	Interrupted Status = "interrupted"
	Canceled    Status = "canceled"
)

type Job struct {
	ID               string     `json:"id"`
	Task             dom.Task   `json:"task"`
	Status           Status     `json:"status"`
	Attempts         int        `json:"attempts"`
	MaxAttempts      int        `json:"max_attempts"`
	AvailableAt      time.Time  `json:"available_at"`
	LeaseOwner       string     `json:"lease_owner,omitempty"`
	LeaseUntil       *time.Time `json:"lease_until,omitempty"`
	ExecutionStarted bool       `json:"execution_started"`
	LastError        string     `json:"last_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (j Job) validate() error {
	if j.ID == "" || j.MaxAttempts < 1 {
		return errors.New("invalid scheduler job")
	}
	return j.Task.Validate()
}

type Store interface {
	Enqueue(context.Context, dom.Task, string, int) (Job, error)
	Claim(context.Context, string, time.Duration) (Job, error)
	MarkStarted(context.Context, string, string) error
	Heartbeat(context.Context, string, string, time.Duration) error
	Complete(context.Context, string, string) error
	Fail(context.Context, string, string, error) error
	Get(context.Context, string) (Job, error)
	RecoverExpired(context.Context) (int64, int64, error)
	Requeue(context.Context, string) (Job, error)
	CancelTask(context.Context, string) error
}

type Handler interface {
	Run(context.Context, dom.Task) (dom.Result, error)
}
type JobPayload struct {
	Task   dom.Task   `json:"task"`
	Result dom.Result `json:"result,omitempty"`
}

func encodeTask(t dom.Task) (string, error) { b, e := json.Marshal(t); return string(b), e }
