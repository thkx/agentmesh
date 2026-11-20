package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	coding "github.com/thkx/agentmesh/internal/coding"
	execdom "github.com/thkx/agentmesh/internal/domain/execution"
	execmanager "github.com/thkx/agentmesh/internal/execution/manager"
)

// ManagedCodingRunner runs workflow Coding activities through the canonical
// Execution Manager. The deterministic task ID and idempotency key make worker
// retries safe; interrupted outcomes are surfaced and never blindly replayed.
type ManagedCodingRunner struct {
	manager *execmanager.Manager
}

func NewManagedCodingRunner(manager *execmanager.Manager) (*ManagedCodingRunner, error) {
	if manager == nil {
		return nil, errors.New("workflow coding execution manager is nil")
	}
	return &ManagedCodingRunner{manager: manager}, nil
}

func (r *ManagedCodingRunner) RunCoding(ctx context.Context, taskID string, input json.RawMessage) (json.RawMessage, error) {
	if taskID == "" || len(input) == 0 || !json.Valid(input) {
		return nil, errors.New("invalid workflow coding activity")
	}
	entity, err := r.manager.Submit(ctx, execmanager.SubmitRequest{
		Kind: execdom.KindCoding, Runtime: execdom.RuntimeCoding, TaskID: taskID,
		Input: input, IdempotencyKey: "workflow-coding:" + taskID,
	})
	if err != nil && entity.ID == "" {
		return nil, fmt.Errorf("submit workflow coding activity: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("workflow coding execution requires reconciliation: %w", err)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			_ = r.manager.Cancel(context.WithoutCancel(ctx), entity.ID)
			return nil, ctx.Err()
		}
		observed, observeErr := r.manager.Get(ctx, entity.ID)
		if observeErr != nil {
			return nil, fmt.Errorf("observe workflow coding execution %s: %w", entity.ID, observeErr)
		}
		entity = observed
		switch entity.Status {
		case execdom.LifecycleSucceeded:
			result, marshalErr := json.Marshal(map[string]any{
				"execution_id": entity.ID, "task_id": taskID,
				"coding_result": json.RawMessage(entity.Result),
			})
			return result, marshalErr
		case execdom.LifecycleFailed:
			return nil, fmt.Errorf("coding task %s failed: %s", taskID, entity.Error)
		case execdom.LifecycleCanceled:
			return nil, fmt.Errorf("coding task %s was canceled", taskID)
		case execdom.LifecycleTimedOut:
			return nil, fmt.Errorf("coding task %s timed out: %s", taskID, entity.Error)
		case execdom.LifecycleInterrupted, execdom.LifecycleNeedsReconciliation:
			return nil, fmt.Errorf("coding task %s has an indeterminate outcome (%s); refusing automatic replay", taskID, entity.Status)
		}
		select {
		case <-ctx.Done():
			_ = r.manager.Cancel(context.WithoutCancel(ctx), entity.ID)
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
