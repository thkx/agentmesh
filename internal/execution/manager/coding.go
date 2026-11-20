package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/thkx/agentmesh/internal/coding"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

type CodingService interface {
	Create(context.Context, coding.CreateRequest) (coding.Task, error)
	Get(context.Context, string) (coding.Task, error)
	Start(context.Context, string) (coding.Task, error)
	Cancel(string) error
}

type CodingAttemptStore interface {
	ListAttempts(context.Context, string) ([]dom.Attempt, error)
	CreateAttempt(context.Context, dom.Attempt) (dom.Attempt, error)
}
type CodingAdapter struct {
	service  CodingService
	attempts CodingAttemptStore
}

func NewCodingAdapter(service CodingService, stores ...CodingAttemptStore) (*CodingAdapter, error) {
	if service == nil {
		return nil, errors.New("coding execution service is nil")
	}
	adapter := &CodingAdapter{service: service}
	if len(stores) > 0 {
		adapter.attempts = stores[0]
	}
	return adapter, nil
}

func (a *CodingAdapter) Submit(ctx context.Context, e dom.Entity) (string, error) {
	var request coding.CreateRequest
	if err := json.Unmarshal(e.Input, &request); err != nil {
		return "", fmt.Errorf("decode coding execution input: %w", err)
	}
	request.ExecutionID = e.ID
	request.AttemptID = fmt.Sprintf("%s-a-1", e.ID)
	if request.ID == "" {
		request.ID = e.TaskID
	}
	if request.ID == "" {
		request.ID = e.ID
	}
	if e.TaskID != "" && request.ID != e.TaskID {
		return "", dom.ErrInvalidEntity
	}
	task, err := a.service.Get(ctx, request.ID)
	if errors.Is(err, coding.ErrNotFound) {
		task, err = a.service.Create(ctx, request)
	}
	if err != nil {
		return "", err
	}
	if binder, ok := a.service.(interface {
		BindExecution(context.Context, string, string, string) error
	}); ok {
		if err := binder.BindExecution(ctx, task.ID, e.ID, request.AttemptID); err != nil {
			return "", err
		}
	}
	if task.Status == coding.StatusPending {
		if _, err = a.service.Start(ctx, task.ID); err != nil {
			return "", err
		}
	}
	return task.ID, nil
}

func (a *CodingAdapter) Observe(ctx context.Context, e dom.Entity) (Observation, error) {
	id := e.RuntimeRef
	if id == "" {
		id = e.TaskID
	}
	task, err := a.service.Get(ctx, id)
	if err != nil {
		return Observation{}, err
	}
	// Pending Coding Tasks are durable in SQLite. Re-scheduling from Observe
	// makes tasks that were waiting for a local concurrency slot survive restart.
	if task.Status == coding.StatusPending {
		if _, err = a.service.Start(ctx, id); err != nil {
			return Observation{}, err
		}
		task, err = a.service.Get(ctx, id)
		if err != nil {
			return Observation{}, err
		}
	}
	attemptNotStarted := task.Status == coding.StatusCanceled && task.StartedAt.IsZero()
	if a.attempts != nil && !attemptNotStarted && (task.Status == coding.StatusSucceeded || task.Status == coding.StatusFailed || task.Status == coding.StatusCanceled || task.Status == coding.StatusInterrupted) {
		attempts, attemptErr := a.attempts.ListAttempts(ctx, e.ID)
		if attemptErr != nil {
			return Observation{}, attemptErr
		}
		if len(attempts) == 0 {
			started := task.StartedAt
			if started.IsZero() {
				started = task.CreatedAt
			}
			_, attemptErr = a.attempts.CreateAttempt(ctx, dom.Attempt{
				ID: task.AttemptID, ExecutionID: e.ID, Number: 1, Runtime: e.Runtime,
				RuntimeRef: task.ID, Status: dom.AttemptRunning, Input: e.Input, StartedAt: started,
			})
			if attemptErr != nil {
				return Observation{}, attemptErr
			}
		}
	}
	if task.Status == coding.StatusSucceeded {
		if finalizer, ok := a.service.(interface {
			PublishArtifacts(context.Context, string) error
		}); ok {
			if err := finalizer.PublishArtifacts(ctx, task.ID); err != nil {
				return Observation{}, fmt.Errorf("publish coding artifact evidence: %w", err)
			}
		}
	}
	var status dom.LifecycleStatus
	switch task.Status {
	case coding.StatusPending:
		status = dom.LifecycleQueued
	case coding.StatusRunning, coding.StatusTesting, coding.StatusRepairing:
		status = dom.LifecycleRunning
	case coding.StatusSucceeded:
		status = dom.LifecycleSucceeded
	case coding.StatusFailed:
		status = dom.LifecycleFailed
	case coding.StatusCanceled:
		status = dom.LifecycleCanceled
	case coding.StatusInterrupted:
		status = dom.LifecycleInterrupted
	default:
		return Observation{}, fmt.Errorf("unknown coding task status %q", task.Status)
	}
	result, _ := json.Marshal(task)
	return Observation{Status: status, Attempt: 1, AttemptNotStarted: attemptNotStarted, Result: result, Error: task.Error}, nil
}

func (a *CodingAdapter) Cancel(ctx context.Context, e dom.Entity) error {
	id := e.RuntimeRef
	if id == "" {
		id = e.TaskID
	}
	if service, ok := a.service.(interface {
		CancelContext(context.Context, string) error
	}); ok {
		return service.CancelContext(ctx, id)
	}
	return a.service.Cancel(id)
}
