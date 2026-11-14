package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
	"github.com/thkx/agentmesh/internal/execution/repository"
)

var (
	ErrAdapterNotRegistered = errors.New("execution runtime adapter not registered")
	ErrInvalidRequest       = errors.New("invalid execution submission")
)

type Observation struct {
	Status  dom.LifecycleStatus
	Attempt int
	Result  json.RawMessage
	Error   string
}

type Adapter interface {
	Submit(context.Context, dom.Entity) (string, error)
	Observe(context.Context, dom.Entity) (Observation, error)
	Cancel(context.Context, dom.Entity) error
}

type SubmitRequest struct {
	Kind           dom.Kind
	Runtime        dom.Runtime
	TaskID         string
	Input          json.RawMessage
	IdempotencyKey string
}

type Manager struct {
	store    repository.Store
	adapters map[dom.Runtime]Adapter
}

func New(store repository.Store) (*Manager, error) {
	if store == nil {
		return nil, errors.New("execution store is nil")
	}
	return &Manager{store: store, adapters: make(map[dom.Runtime]Adapter)}, nil
}

func (m *Manager) Register(runtime dom.Runtime, adapter Adapter) error {
	if strings.TrimSpace(string(runtime)) == "" || adapter == nil {
		return ErrInvalidRequest
	}
	if _, exists := m.adapters[runtime]; exists {
		return fmt.Errorf("adapter already registered for runtime %q", runtime)
	}
	m.adapters[runtime] = adapter
	return nil
}

func (m *Manager) Submit(ctx context.Context, in SubmitRequest) (dom.Entity, error) {
	if !validKindRuntime(in.Kind, in.Runtime) || len(in.Input) == 0 || !json.Valid(in.Input) || len(in.Input) > 1<<20 || len(in.TaskID) > 128 || len(in.IdempotencyKey) > 256 {
		return dom.Entity{}, ErrInvalidRequest
	}
	if in.TaskID == "" && (in.Runtime == dom.RuntimeAgent || in.Runtime == dom.RuntimeCoding) {
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(in.Input, &identity) == nil {
			in.TaskID = identity.ID
		}
	}
	adapter, ok := m.adapters[in.Runtime]
	if !ok {
		return dom.Entity{}, ErrAdapterNotRegistered
	}
	if in.IdempotencyKey != "" {
		old, err := m.store.GetByIdempotencyKey(ctx, in.IdempotencyKey)
		if err == nil {
			if old.Kind != in.Kind || old.Runtime != in.Runtime || old.TaskID != in.TaskID || string(old.Input) != string(in.Input) {
				return dom.Entity{}, dom.ErrConflict
			}
			return m.Get(ctx, old.ID)
		}
		if !errors.Is(err, dom.ErrNotFound) {
			return dom.Entity{}, err
		}
	}
	now := time.Now().UTC()
	e := dom.Entity{
		ID: uuid.NewString(), Kind: in.Kind, Runtime: in.Runtime, TaskID: in.TaskID,
		Status: dom.LifecycleQueued, Input: append(json.RawMessage(nil), in.Input...),
		IdempotencyKey: in.IdempotencyKey, CreatedAt: now, UpdatedAt: now,
	}
	created, err := m.store.Create(ctx, e)
	if err != nil {
		return dom.Entity{}, err
	}
	if created.ID != e.ID {
		return m.Get(ctx, created.ID)
	}
	ref, err := adapter.Submit(ctx, created)
	if err != nil {
		expected := []dom.LifecycleStatus{dom.LifecycleQueued}
		if in.Runtime == dom.RuntimeCLI {
			expected = append(expected, dom.LifecycleRunning)
		}
		failed, transitionErr := m.store.Transition(context.Background(), created.ID, expected, dom.LifecycleFailed, err.Error(), nil)
		return failed, errors.Join(err, transitionErr)
	}
	if ref != "" {
		created, err = m.store.BindRuntimeRef(ctx, created.ID, ref)
		if err != nil {
			return created, fmt.Errorf("bind runtime reference; runtime may already have accepted the execution: %w", err)
		}
	}
	created.Attempts, _ = m.store.ListAttempts(ctx, created.ID)
	return created, nil
}

func (m *Manager) Get(ctx context.Context, id string) (dom.Entity, error) {
	e, err := m.store.Get(ctx, id)
	if err != nil {
		return dom.Entity{}, err
	}
	adapter, ok := m.adapters[e.Runtime]
	if !ok {
		e.Attempts, _ = m.store.ListAttempts(ctx, e.ID)
		return e, nil
	}
	observed, err := adapter.Observe(ctx, e)
	if err != nil {
		return e, err
	}
	if observed.Status == "" {
		e.Attempts, _ = m.store.ListAttempts(ctx, e.ID)
		return e, nil
	}
	attempts, attemptErr := m.store.ListAttempts(ctx, e.ID)
	if attemptErr != nil {
		return e, attemptErr
	}

	// A queued observation means the runtime has accepted or retained the work,
	// but no execution attempt has started. Never synthesize a running attempt
	// until the runtime reports running or a terminal outcome.
	needsAttempt := observed.Status == dom.LifecycleRunning ||
		(observed.Status != dom.LifecycleQueued && isTerminalOrRetry(observed.Status))
	if needsAttempt {
		number := observed.Attempt
		if number < 1 {
			number = 1
			if len(attempts) > 0 {
				number = attempts[len(attempts)-1].Number
			}
		}
		found := false
		for _, a := range attempts {
			if a.Number == number {
				found = true
				break
			}
		}
		if !found {
			now := time.Now().UTC()
			_, createErr := m.store.CreateAttempt(ctx, dom.Attempt{
				ID: fmt.Sprintf("%s-a-%d", e.ID, number), ExecutionID: e.ID, Number: number,
				Runtime: e.Runtime, RuntimeRef: e.RuntimeRef, Status: dom.AttemptRunning,
				Input: e.Input, StartedAt: now,
			})
			if createErr != nil {
				return e, createErr
			}
			attempts, attemptErr = m.store.ListAttempts(ctx, e.ID)
			if attemptErr != nil {
				return e, attemptErr
			}
		}
	}
	if observed.Status == e.Status {
		e.Attempts = attempts
		return e, nil
	}
	if !dom.CanTransition(e.Status, observed.Status) {
		return e, dom.ErrInvalidTransition
	}
	if observed.Status != dom.LifecycleRunning && len(attempts) > 0 {
		last := attempts[len(attempts)-1]
		if last.Status == dom.AttemptRunning {
			attemptStatus := toAttemptStatus(observed.Status)
			_, finishErr := m.store.FinishAttempt(ctx, last.ID, attemptStatus, e.RuntimeRef, observed.Result, observed.Error)
			if finishErr != nil && !errors.Is(finishErr, dom.ErrInvalidTransition) {
				return e, finishErr
			}
		}
	}
	updated, transitionErr := m.store.Transition(ctx, e.ID, []dom.LifecycleStatus{e.Status}, observed.Status, observed.Error, observed.Result)
	if transitionErr != nil {
		return e, transitionErr
	}
	updated.Attempts, _ = m.store.ListAttempts(ctx, e.ID)
	return updated, nil
}

func (m *Manager) Cancel(ctx context.Context, id string) error {
	e, err := m.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if e.Status != dom.LifecycleQueued && e.Status != dom.LifecycleRunning {
		return dom.ErrInvalidTransition
	}
	adapter, ok := m.adapters[e.Runtime]
	if !ok {
		return ErrAdapterNotRegistered
	}
	return adapter.Cancel(ctx, e)
}

func (m *Manager) List(ctx context.Context, f dom.ListFilter) ([]dom.Entity, error) {
	return m.store.List(ctx, f)
}

type ReconcileReport struct {
	Scanned         int `json:"scanned"`
	Recovered       int `json:"recovered"`
	NeedsAttention int `json:"needs_attention"`
}

// Reconcile repairs queued submit crash windows and observes active executions.
// It never resubmits a running execution whose outcome may be ambiguous.
func (m *Manager) Reconcile(ctx context.Context, limit int) (ReconcileReport, error) {
	var report ReconcileReport
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	queued, err := m.store.List(ctx, dom.ListFilter{Status: dom.LifecycleQueued, Limit: limit})
	if err != nil {
		return report, err
	}
	running, err := m.store.List(ctx, dom.ListFilter{Status: dom.LifecycleRunning, Limit: limit})
	if err != nil {
		return report, err
	}
	items := append(queued, running...)
	for _, item := range items {
		report.Scanned++
		adapter, ok := m.adapters[item.Runtime]
		if !ok {
			report.NeedsAttention++
			continue
		}
		if item.Status == dom.LifecycleQueued {
			ref, submitErr := adapter.Submit(ctx, item)
			if submitErr != nil {
				report.NeedsAttention++
				continue
			}
			if ref != "" && item.RuntimeRef == "" {
				if _, bindErr := m.store.BindRuntimeRef(ctx, item.ID, ref); bindErr != nil {
					report.NeedsAttention++
					continue
				}
			}
		}
		updated, getErr := m.Get(ctx, item.ID)
		if getErr != nil {
			report.NeedsAttention++
			continue
		}
		if updated.Status != item.Status {
			report.Recovered++
		}
	}
	return report, nil
}

func validKindRuntime(kind dom.Kind, runtime dom.Runtime) bool {
	switch kind {
	case dom.KindAgent:
		return runtime == dom.RuntimeAgent
	case dom.KindCoding:
		return runtime == dom.RuntimeCoding
	case dom.KindWorkflow:
		return runtime == dom.RuntimeWorkflow
	case dom.KindCLI:
		return runtime == dom.RuntimeCLI
	default:
		return false
	}
}

func isTerminalOrRetry(status dom.LifecycleStatus) bool {
	return status == dom.LifecycleQueued || status == dom.LifecycleSucceeded || status == dom.LifecycleFailed ||
		status == dom.LifecycleCanceled || status == dom.LifecycleTimedOut ||
		status == dom.LifecycleInterrupted || status == dom.LifecycleNeedsReconciliation
}

func toAttemptStatus(status dom.LifecycleStatus) dom.AttemptStatus {
	switch status {
	case dom.LifecycleSucceeded:
		return dom.AttemptSucceeded
	case dom.LifecycleCanceled:
		return dom.AttemptCanceled
	case dom.LifecycleTimedOut:
		return dom.AttemptTimedOut
	case dom.LifecycleInterrupted, dom.LifecycleNeedsReconciliation:
		return dom.AttemptUnknown
	default:
		return dom.AttemptFailed
	}
}
