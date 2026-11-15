package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	dom "github.com/thkx/agentmesh/internal/domain/execution"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
)

type WorkflowEngine interface {
	StartQueued(context.Context, string, string, map[string]any, int) (wf.Run, error)
	GetRun(context.Context, string) (wf.Run, error)
	CancelRun(context.Context, string) error
}

type WorkflowAdapter struct {
	engine      WorkflowEngine
	maxAttempts int
}

func NewWorkflowAdapter(engine WorkflowEngine, maxAttempts int) (*WorkflowAdapter, error) {
	if engine == nil {
		return nil, errors.New("workflow execution engine is nil")
	}
	if maxAttempts < 1 {
		maxAttempts = 3
	}
	return &WorkflowAdapter{engine: engine, maxAttempts: maxAttempts}, nil
}

func (a *WorkflowAdapter) Submit(ctx context.Context, e dom.Entity) (string, error) {
	var request struct {
		WorkflowID string         `json:"workflow_id"`
		Input      map[string]any `json:"input"`
	}
	if err := json.Unmarshal(e.Input, &request); err != nil {
		return "", fmt.Errorf("decode workflow execution input: %w", err)
	}
	if request.WorkflowID == "" {
		return "", dom.ErrInvalidEntity
	}
	runID := e.TaskID
	if runID == "" {
		runID = e.ID
	}
	run, err := a.engine.GetRun(ctx, runID)
	if err == nil {
		if run.WorkflowID != request.WorkflowID {
			return "", dom.ErrConflict
		}
		return run.ID, nil
	}
	if !errors.Is(err, wf.ErrNotFound) {
		return "", fmt.Errorf("lookup workflow run before submit: %w", err)
	}
	run, err = a.engine.StartQueued(ctx, request.WorkflowID, runID, request.Input, a.maxAttempts)
	if err != nil {
		return "", err
	}
	return run.ID, nil
}

func (a *WorkflowAdapter) Observe(ctx context.Context, e dom.Entity) (Observation, error) {
	id := e.RuntimeRef
	if id == "" {
		id = e.TaskID
	}
	run, err := a.engine.GetRun(ctx, id)
	if err != nil {
		return Observation{}, err
	}
	var status dom.LifecycleStatus
	switch run.Status {
	case wf.RunQueued:
		status = dom.LifecycleQueued
	case wf.RunRunning, wf.RunWaitingApproval:
		status = dom.LifecycleRunning
	case wf.RunSucceeded:
		status = dom.LifecycleSucceeded
	case wf.RunFailed, wf.RunRejected:
		status = dom.LifecycleFailed
	case wf.RunCanceled:
		status = dom.LifecycleCanceled
	default:
		return Observation{}, fmt.Errorf("unknown workflow run status %q", run.Status)
	}
	result, _ := json.Marshal(run)
	return Observation{Status: status, Attempt: 1, Result: result, Error: run.Error}, nil
}

func (a *WorkflowAdapter) Cancel(ctx context.Context, e dom.Entity) error {
	id := e.RuntimeRef
	if id == "" {
		id = e.TaskID
	}
	return a.engine.CancelRun(ctx, id)
}
