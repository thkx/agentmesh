package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	execdom "github.com/thkx/agentmesh/internal/domain/execution"
	execmanager "github.com/thkx/agentmesh/internal/execution/manager"
	workflowruntime "github.com/thkx/agentmesh/internal/workflow/runtime"
)

type blockingCodingExecutionManager struct {
	getStarted chan struct{}
	cancelled  int
}

func (m *blockingCodingExecutionManager) Submit(context.Context, execmanager.SubmitRequest) (execdom.Entity, error) {
	return execdom.Entity{ID: "coding-execution", Status: execdom.LifecycleQueued}, nil
}
func (m *blockingCodingExecutionManager) Get(ctx context.Context, _ string) (execdom.Entity, error) {
	select {
	case m.getStarted <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return execdom.Entity{}, ctx.Err()
}
func (m *blockingCodingExecutionManager) Cancel(context.Context, string) error {
	m.cancelled++
	return nil
}

func TestWorkflowCodingRunnerCancelsOnlyForExplicitWorkflowCancellation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "worker_shutdown", true: "user_cancel"}[explicit], func(t *testing.T) {
			manager := &blockingCodingExecutionManager{getStarted: make(chan struct{}, 1)}
			runner, err := newWorkflowCodingRunner(manager)
			if err != nil {
				t.Fatal(err)
			}
			base := workflowruntime.WithCancellationRequestCheck(context.Background(), func() bool { return explicit })
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, runErr := runner.RunCoding(ctx, "stable-task", json.RawMessage(`{}`))
				done <- runErr
			}()
			select {
			case <-manager.getStarted:
			case <-time.After(2 * time.Second):
				t.Fatal("coding execution observation did not start")
			}
			cancel()
			select {
			case runErr := <-done:
				if !errors.Is(runErr, context.Canceled) {
					t.Fatalf("expected context cancellation, got %v", runErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("workflow coding runner did not stop")
			}
			expected := 0
			if explicit {
				expected = 1
			}
			if manager.cancelled != expected {
				t.Fatalf("coding cancel calls=%d, want %d", manager.cancelled, expected)
			}
		})
	}
}
