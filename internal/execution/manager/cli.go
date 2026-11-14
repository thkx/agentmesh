package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/thkx/agentmesh/internal/agent/executor"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
	"github.com/thkx/agentmesh/internal/execution/repository"
)

// CLIAdapter adapts the CLI executor to the unified execution lifecycle.
// CLI processes are local to this process; after a restart, a running execution
// is marked interrupted and is never replayed automatically.
type CLIAdapter struct {
	executor executor.AgentExecutor
	store    repository.Store

	mu      sync.RWMutex
	cancels map[string]context.CancelFunc
	results map[string]Observation

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closed    bool
	done      chan struct{}
	closeOnce sync.Once
}

func NewCLIAdapter(exec executor.AgentExecutor, store repository.Store) (*CLIAdapter, error) {
	if exec == nil || store == nil {
		return nil, errors.New("CLI execution adapter dependencies are nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &CLIAdapter{
		executor: exec,
		store:    store,
		cancels:  make(map[string]context.CancelFunc),
		results:  make(map[string]Observation),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}, nil
}

func (a *CLIAdapter) Submit(ctx context.Context, e dom.Entity) (string, error) {
	var request dom.Request
	if err := json.Unmarshal(e.Input, &request); err != nil {
		return "", fmt.Errorf("decode CLI execution input: %w", err)
	}
	if request.ID == "" {
		request.ID = e.TaskID
	}
	if request.ID == "" {
		request.ID = e.ID
	}
	if err := request.Validate(); err != nil {
		return "", err
	}
	if e.TaskID != "" && request.ID != e.TaskID {
		return "", dom.ErrInvalidEntity
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Serialize submissions and WaitGroup.Add against CloseContext. Persist the
	// attempt first so a failed attempt insert cannot strand a running execution.
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return "", errors.New("CLI execution adapter is closed")
	}
	current, err := a.store.Get(ctx, e.ID)
	if err != nil {
		a.mu.Unlock()
		return "", err
	}
	if current.Status == dom.LifecycleRunning {
		// A concurrent submit/reconcile may have started this execution already.
		// Reuse it, or expose it as interrupted after process loss; never spawn
		// a second CLI process for the same durable execution.
		a.mu.Unlock()
		return request.ID, nil
	}
	if current.Status != dom.LifecycleQueued {
		a.mu.Unlock()
		return "", dom.ErrInvalidTransition
	}
	attemptID := e.ID + "-a-1"
	now := time.Now().UTC()
	attempt := dom.Attempt{
		ID: attemptID, ExecutionID: e.ID, Number: 1, Runtime: dom.RuntimeCLI,
		RuntimeRef: request.ID, Status: dom.AttemptRunning, Input: e.Input,
		StartedAt: now,
	}
	if _, err := a.store.CreateAttempt(ctx, attempt); err != nil {
		a.mu.Unlock()
		return "", fmt.Errorf("create CLI execution attempt: %w", err)
	}
	if _, err := a.store.Transition(ctx, e.ID, []dom.LifecycleStatus{dom.LifecycleQueued}, dom.LifecycleRunning, "", nil); err != nil {
		a.mu.Unlock()
		return "", err
	}

	runCtx, cancel := context.WithCancel(a.ctx)
	if request.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(a.ctx, request.Timeout)
	}
	a.cancels[request.ID] = cancel
	a.results[request.ID] = Observation{Status: dom.LifecycleRunning, Attempt: 1}
	a.wg.Add(1)
	a.mu.Unlock()

	go func() {
		defer a.wg.Done()
		result, runErr := a.executor.Execute(runCtx, request)
		cancel()

		var raw []byte
		target := dom.LifecycleFailed
		attemptStatus := dom.AttemptFailed
		message := ""
		if result != nil {
			raw, _ = json.Marshal(result)
			switch result.Status {
			case dom.StatusSucceeded:
				target, attemptStatus = dom.LifecycleSucceeded, dom.AttemptSucceeded
			case dom.StatusCanceled:
				target, attemptStatus = dom.LifecycleCanceled, dom.AttemptCanceled
			case dom.StatusTimedOut:
				target, attemptStatus = dom.LifecycleTimedOut, dom.AttemptTimedOut
			default:
				target, attemptStatus = dom.LifecycleFailed, dom.AttemptFailed
			}
		}
		if runErr != nil {
			message = runErr.Error()
			target, attemptStatus = dom.LifecycleFailed, dom.AttemptFailed
			if errors.Is(runErr, context.Canceled) {
				target, attemptStatus = dom.LifecycleCanceled, dom.AttemptCanceled
			}
			if errors.Is(runErr, context.DeadlineExceeded) {
				target, attemptStatus = dom.LifecycleTimedOut, dom.AttemptTimedOut
			}
		}

		// Shutdown cancels the execution context, not the persistence context.
		// Give final status writes a bounded opportunity to reach SQLite.
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer persistCancel()
		_, attemptErr := a.store.FinishAttempt(persistCtx, attemptID, attemptStatus, request.ID, raw, message)
		if attemptErr != nil {
			message = fmt.Sprintf("finish CLI attempt: %v", attemptErr)
			target = dom.LifecycleNeedsReconciliation
			_, transitionErr := a.store.Transition(persistCtx, e.ID, []dom.LifecycleStatus{dom.LifecycleRunning}, target, message, raw)
			if transitionErr != nil {
				message += fmt.Sprintf("; mark execution for reconciliation: %v", transitionErr)
			}
		} else {
			_, transitionErr := a.store.Transition(persistCtx, e.ID, []dom.LifecycleStatus{dom.LifecycleRunning}, target, message, raw)
			if transitionErr != nil {
				latest, readErr := a.store.Get(persistCtx, e.ID)
				if readErr == nil && latest.Status != dom.LifecycleRunning {
					target = latest.Status
					if len(latest.Result) > 0 { raw = latest.Result }
					if latest.Error != "" { message = latest.Error }
				} else {
					target = dom.LifecycleNeedsReconciliation
					message = fmt.Sprintf("persist CLI execution result: %v", transitionErr)
					_, fallbackErr := a.store.Transition(persistCtx, e.ID, []dom.LifecycleStatus{dom.LifecycleRunning}, target, message, raw)
					if fallbackErr != nil {
						message += fmt.Sprintf("; mark execution for reconciliation: %v", fallbackErr)
					}
				}
			}
		}

		a.mu.Lock()
		delete(a.cancels, request.ID)
		a.results[request.ID] = Observation{
			Status: target, Attempt: 1, Result: raw, Error: message,
		}
		a.mu.Unlock()
	}()
	return request.ID, nil
}

func (a *CLIAdapter) Observe(_ context.Context, e dom.Entity) (Observation, error) {
	id := e.RuntimeRef
	if id == "" {
		id = e.TaskID
	}
	a.mu.RLock()
	observation, ok := a.results[id]
	a.mu.RUnlock()
	if ok {
		return observation, nil
	}
	if e.Status == dom.LifecycleRunning {
		return Observation{
			Status: dom.LifecycleInterrupted,
			Error:  "CLI process state was lost; automatic replay is disabled",
		}, nil
	}
	return Observation{Status: e.Status, Result: e.Result, Error: e.Error}, nil
}

func (a *CLIAdapter) Cancel(ctx context.Context, e dom.Entity) error {
	id := e.RuntimeRef
	if id == "" {
		id = e.TaskID
	}
	a.mu.Lock()
	if cancel, ok := a.cancels[id]; ok {
		a.mu.Unlock()
		cancel()
		return nil
	}
	current, err := a.store.Get(ctx, e.ID)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	if current.Status == dom.LifecycleQueued {
		_, err = a.store.Transition(ctx, e.ID, []dom.LifecycleStatus{dom.LifecycleQueued}, dom.LifecycleCanceled, "canceled before CLI process start", nil)
		a.mu.Unlock()
		return err
	}
	a.mu.Unlock()
	if current.Status != dom.LifecycleRunning {
		return dom.ErrInvalidTransition
	}
	return a.executor.Cancel(ctx, id)
}

// CloseContext prevents new work, cancels active CLI executions, and waits for
// each executor to return and persist its final execution and attempt state.
func (a *CLIAdapter) CloseContext(ctx context.Context) error {
	a.mu.Lock()
	a.closed = true
	a.cancel()
	a.mu.Unlock()

	a.closeOnce.Do(func() {
		go func() {
			a.wg.Wait()
			close(a.done)
		}()
	})
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *CLIAdapter) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return a.CloseContext(ctx)
}
