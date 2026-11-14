package manager

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/agent/executor"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

type blockingCLIExecutor struct {
	started chan struct{}
}

func (e *blockingCLIExecutor) Execute(ctx context.Context, request dom.Request) (*dom.Result, error) {
	close(e.started)
	<-ctx.Done()
	return &dom.Result{
		ID: request.ID, Provider: request.Provider, Status: dom.StatusCanceled,
		StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
	}, ctx.Err()
}

func (e *blockingCLIExecutor) Cancel(context.Context, string) error { return nil }

var _ executor.AgentExecutor = (*blockingCLIExecutor)(nil)

func TestCLIAdapterCloseWaitsForCancellationAndPersistence(t *testing.T) {
	store := newMemoryStore()
	now := time.Now().UTC()
	input, err := json.Marshal(dom.Request{
		ID: "cli-task-1", Provider: dom.ProviderCodex, Prompt: "test",
		WorkspacePath: t.TempDir(), PermissionMode: dom.PermissionWorkspaceWrite,
	})
	if err != nil { t.Fatal(err) }
	entity := dom.Entity{
		ID: "execution-cli-1", Kind: dom.KindCLI, Runtime: dom.RuntimeCLI,
		TaskID: "cli-task-1", Status: dom.LifecycleQueued,
		Input: input, CreatedAt: now, UpdatedAt: now,
	}
	store.values[entity.ID] = entity
	exec := &blockingCLIExecutor{started: make(chan struct{})}
	adapter, err := NewCLIAdapter(exec, store)
	if err != nil { t.Fatal(err) }
	if _, err := adapter.Submit(context.Background(), entity); err != nil { t.Fatal(err) }
	select {
	case <-exec.started:
	case <-time.After(2*time.Second):
		t.Fatal("CLI executor did not start")
	}
	// A reconciliation racing the initial submit must reuse the active process.
	if ref, err := adapter.Submit(context.Background(), entity); err != nil || ref != "cli-task-1" {
		t.Fatalf("duplicate submit ref=%q err=%v", ref, err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := adapter.CloseContext(closeCtx); err != nil { t.Fatalf("close adapter: %v", err) }
	got, err := store.Get(context.Background(), entity.ID)
	if err != nil { t.Fatal(err) }
	if got.Status != dom.LifecycleCanceled {
		t.Fatalf("execution status=%s, want canceled (error=%s)", got.Status, got.Error)
	}
	attempts, err := store.ListAttempts(context.Background(), entity.ID)
	if err != nil { t.Fatal(err) }
	if len(attempts) != 1 || attempts[0].Status != dom.AttemptCanceled {
		t.Fatalf("attempts=%+v, want one canceled attempt", attempts)
	}
	if err := adapter.CloseContext(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("second close: %v", err)
	}
}


func TestCLIAdapterCancelsQueuedExecutionWithoutLaunchingProcess(t *testing.T) {
	store := newMemoryStore()
	now := time.Now().UTC()
	entity := dom.Entity{
		ID: "execution-cli-queued", Kind: dom.KindCLI, Runtime: dom.RuntimeCLI,
		TaskID: "cli-task-queued", Status: dom.LifecycleQueued,
		Input: json.RawMessage(`{"id":"cli-task-queued"}`), CreatedAt: now, UpdatedAt: now,
	}
	store.values[entity.ID] = entity
	exec := &blockingCLIExecutor{started: make(chan struct{})}
	adapter, err := NewCLIAdapter(exec, store)
	if err != nil { t.Fatal(err) }
	if err := adapter.Cancel(context.Background(), entity); err != nil { t.Fatal(err) }
	got, err := store.Get(context.Background(), entity.ID)
	if err != nil { t.Fatal(err) }
	if got.Status != dom.LifecycleCanceled { t.Fatalf("status=%s, want canceled", got.Status) }
	select {
	case <-exec.started:
		t.Fatal("queued cancellation unexpectedly launched the CLI executor")
	default:
	}
	if err := adapter.CloseContext(context.Background()); err != nil { t.Fatal(err) }
}
