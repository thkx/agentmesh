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
	if in.TaskID == "" && (in.Runtime == dom.RuntimeAgent || in.Runtime == dom.RuntimeCoding || in.Runtime == dom.RuntimeCLI) {
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
		// Submit may have been accepted remotely even when the response was
		// lost. Preserve that uncertainty; never label it failed.
		reason := "runtime submission outcome uncertain: " + err.Error()
		pending, transitionErr := m.store.Transition(context.Background(), created.ID,
			[]dom.LifecycleStatus{dom.LifecycleQueued}, dom.LifecycleNeedsReconciliation, reason, nil)
		return pending, errors.Join(err, transitionErr)
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
	// Persist intent before the external side effect. Adapter success is not
	// proof that the runtime has stopped; Observe owns the final outcome.
	controlStore, ok := m.store.(interface {
		RequestCancel(context.Context, string) error
	})
	if !ok {
		return errors.New("execution store does not support durable cancellation requests")
	}
	if err := controlStore.RequestCancel(ctx, id); err != nil {
		return err
	}
	return adapter.Cancel(ctx, e)
}

// Resolve records an explicit human decision for an execution whose runtime
// outcome could not be determined safely. It never replays the underlying work.
func (m *Manager) Resolve(ctx context.Context, id string, status dom.LifecycleStatus, result json.RawMessage, reason string) (dom.Entity, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 4000 || len(result) > 4<<20 ||
		(len(result) > 0 && !json.Valid(result)) ||
		(status != dom.LifecycleSucceeded && status != dom.LifecycleFailed && status != dom.LifecycleCanceled) ||
		(status == dom.LifecycleSucceeded && len(result) == 0) {
		return dom.Entity{}, ErrInvalidRequest
	}
	current, err := m.store.Get(ctx, id)
	if err != nil {
		return dom.Entity{}, err
	}
	if current.Status != dom.LifecycleInterrupted && current.Status != dom.LifecycleNeedsReconciliation {
		return dom.Entity{}, dom.ErrInvalidTransition
	}
	attempts, err := m.store.ListAttempts(ctx, id)
	if err != nil {
		return dom.Entity{}, err
	}
	for _, attempt := range attempts {
		if attempt.Status != dom.AttemptRunning {
			continue
		}
		_, finishErr := m.store.FinishAttempt(ctx, attempt.ID, dom.AttemptUnknown, attempt.RuntimeRef, nil,
			"runtime outcome unresolved; execution resolved by operator")
		if finishErr != nil && !errors.Is(finishErr, dom.ErrInvalidTransition) {
			return dom.Entity{}, finishErr
		}
	}
	updated, err := m.store.Transition(ctx, id,
		[]dom.LifecycleStatus{dom.LifecycleInterrupted, dom.LifecycleNeedsReconciliation},
		status, "operator resolution: "+reason, result)
	if err != nil {
		return dom.Entity{}, err
	}
	updated.Attempts, err = m.store.ListAttempts(ctx, id)
	if err != nil {
		return dom.Entity{}, err
	}
	return updated, nil
}

func (m *Manager) List(ctx context.Context, f dom.ListFilter) ([]dom.Entity, error) {
	return m.store.List(ctx, f)
}

type ReconcileReport struct {
	Scanned        int `json:"scanned"`
	Recovered      int `json:"recovered"`
	NeedsAttention int `json:"needs_attention"`
}

// Reconcile repairs queued submit crash windows and observes active executions.
// It never resubmits a running execution whose outcome may be ambiguous.
func (m *Manager) Reconcile(ctx context.Context, limit int) (ReconcileReport, error) {
	var report ReconcileReport
	if limit <= 0 || limit > 1000 {
		limit = 200
	}

	// Snapshot both status sets before processing so an item is never scanned
	// twice in one pass after a queued-to-running transition.
	var queuedItems, runningItems []dom.Entity
	for _, status := range []dom.LifecycleStatus{dom.LifecycleQueued, dom.LifecycleRunning} {
		offset := 0
		for {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			page, err := m.store.List(ctx, dom.ListFilter{Status: status, Limit: limit, Offset: offset})
			if err != nil {
				return report, err
			}
			if status == dom.LifecycleQueued {
				queuedItems = append(queuedItems, page...)
			} else {
				runningItems = append(runningItems, page...)
			}
			if len(page) < limit {
				break
			}
			offset += len(page)
		}
	}

	items := append(queuedItems, runningItems...)
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Scanned++
		adapter, ok := m.adapters[item.Runtime]
		if !ok {
			report.NeedsAttention++
			if err := m.markNeedsReconciliation(ctx, item, "runtime adapter is not registered during reconciliation"); err != nil {
				return report, err
			}
			continue
		}

		if item.Status == dom.LifecycleQueued {
			// Each adapter must use the durable TaskID/ExecutionID as its
			// idempotency identity. Repeating Submit after a crash is safe only
			// because the runtime recognizes that same identity.
			ref, submitErr := adapter.Submit(ctx, item)
			if submitErr != nil {
				if ctx.Err() != nil {
					return report, ctx.Err()
				}
				report.NeedsAttention++
				if err := m.markNeedsReconciliation(ctx, item,
					"queued submission could not be confirmed: "+submitErr.Error()); err != nil {
					return report, err
				}
				continue
			}
			if ref != "" && item.RuntimeRef == "" {
				if _, bindErr := m.store.BindRuntimeRef(ctx, item.ID, ref); bindErr != nil {
					report.NeedsAttention++
					if err := m.markNeedsReconciliation(ctx, item,
						"runtime accepted submission but reference binding failed: "+bindErr.Error()); err != nil {
						return report, err
					}
					continue
				}
			}
		}

		updated, getErr := m.Get(ctx, item.ID)
		if getErr != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			report.NeedsAttention++
			if err := m.markNeedsReconciliation(ctx, item,
				"runtime observation could not be reconciled: "+getErr.Error()); err != nil {
				return report, err
			}
			continue
		}
		if updated.Status != item.Status {
			report.Recovered++
		}
	}
	return report, nil
}

// markNeedsReconciliation persists why automatic recovery stopped. A
// concurrent worker's accepted terminal fact always wins over this fallback.
func (m *Manager) markNeedsReconciliation(ctx context.Context, item dom.Entity, reason string) error {
	_, err := m.store.Transition(ctx, item.ID,
		[]dom.LifecycleStatus{dom.LifecycleQueued, dom.LifecycleRunning},
		dom.LifecycleNeedsReconciliation, reason, nil)
	if !errors.Is(err, dom.ErrInvalidTransition) {
		return err
	}
	latest, readErr := m.store.Get(ctx, item.ID)
	if readErr != nil {
		return readErr
	}
	switch latest.Status {
	case dom.LifecycleNeedsReconciliation, dom.LifecycleInterrupted, dom.LifecycleSucceeded,
		dom.LifecycleFailed, dom.LifecycleCanceled, dom.LifecycleTimedOut:
		return nil
	default:
		return err
	}
}

func (m *Manager) RunReconciler(ctx context.Context, interval time.Duration, limit int, onError func(error)) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.Reconcile(ctx, limit); err != nil && onError != nil && !errors.Is(err, context.Canceled) {
				onError(err)
			}
		}
	}
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
