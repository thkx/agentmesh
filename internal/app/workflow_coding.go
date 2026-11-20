package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	execdom "github.com/thkx/agentmesh/internal/domain/execution"
	execmanager "github.com/thkx/agentmesh/internal/execution/manager"
	workflowruntime "github.com/thkx/agentmesh/internal/workflow/runtime"
)

type codingExecutionManager interface {
	Submit(context.Context, execmanager.SubmitRequest) (execdom.Entity, error)
	Get(context.Context, string) (execdom.Entity, error)
	Cancel(context.Context, string) error
}

type workflowCodingRunner struct{ manager codingExecutionManager }

func newWorkflowCodingRunner(manager codingExecutionManager) (*workflowCodingRunner, error) {
	if manager == nil {
		return nil, errors.New("workflow coding execution manager is nil")
	}
	return &workflowCodingRunner{manager: manager}, nil
}

// RunCoding submits a durable Coding Execution and observes its terminal state.
// The stable task ID prevents duplicate Coding Tasks when a workflow delivery repeats.
func (r *workflowCodingRunner) RunCoding(ctx context.Context, taskID string, input json.RawMessage) (json.RawMessage, error) {
	if taskID == "" || len(input) == 0 || !json.Valid(input) {
		return nil, errors.New("invalid workflow coding activity")
	}
	entity, err := r.manager.Submit(ctx, execmanager.SubmitRequest{
		Kind: execdom.KindCoding, Runtime: execdom.RuntimeCoding, TaskID: taskID,
		Input: input, IdempotencyKey: "workflow-coding:" + taskID,
	})
	if err != nil {
		if entity.ID != "" {
			return nil, fmt.Errorf("%w: Execution %s for Coding Task %s has an uncertain submit outcome: %v",
				workflowruntime.ErrCodingOutcomeIndeterminate, entity.ID, taskID, err)
		}
		return nil, fmt.Errorf("submit workflow coding activity: %w", err)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			if workflowruntime.IsCancellationRequested(ctx) {
				_ = r.manager.Cancel(context.WithoutCancel(ctx), entity.ID)
			}
			return nil, ctx.Err()
		}
		entity, err = r.manager.Get(ctx, entity.ID)
		if err != nil {
			if ctx.Err() != nil {
				if workflowruntime.IsCancellationRequested(ctx) {
					_ = r.manager.Cancel(context.WithoutCancel(ctx), entity.ID)
				}
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%w: cannot observe Coding Execution %s: %v", workflowruntime.ErrCodingOutcomeIndeterminate, entity.ID, err)
		}
		switch entity.Status {
		case execdom.LifecycleSucceeded:
			out, marshalErr := json.Marshal(map[string]any{"execution_id": entity.ID, "task_id": taskID, "coding_result": json.RawMessage(entity.Result)})
			return out, marshalErr
		case execdom.LifecycleFailed:
			return nil, fmt.Errorf("coding task %s failed: %s", taskID, entity.Error)
		case execdom.LifecycleCanceled:
			return nil, fmt.Errorf("coding task %s was canceled", taskID)
		case execdom.LifecycleTimedOut:
			return nil, fmt.Errorf("coding task %s timed out: %s", taskID, entity.Error)
		case execdom.LifecycleInterrupted, execdom.LifecycleNeedsReconciliation:
			return nil, fmt.Errorf("%w: task %s has status %s; refusing automatic replay", workflowruntime.ErrCodingOutcomeIndeterminate, taskID, entity.Status)
		}
		select {
		case <-ctx.Done():
			if workflowruntime.IsCancellationRequested(ctx) {
				_ = r.manager.Cancel(context.WithoutCancel(ctx), entity.ID)
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
