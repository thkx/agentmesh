package manager

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

type memoryStore struct {
	values         map[string]dom.Entity
	attempts       map[string]dom.Attempt
	cancelRequests []string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{values: map[string]dom.Entity{}, attempts: map[string]dom.Attempt{}}
}

func (s *memoryStore) Create(_ context.Context, e dom.Entity) (dom.Entity, error) {
	s.values[e.ID] = e
	return e, nil
}

func (s *memoryStore) Get(_ context.Context, id string) (dom.Entity, error) {
	e, ok := s.values[id]
	if !ok {
		return dom.Entity{}, dom.ErrNotFound
	}
	return e, nil
}

func (s *memoryStore) GetByIdempotencyKey(_ context.Context, key string) (dom.Entity, error) {
	for _, e := range s.values {
		if e.IdempotencyKey == key {
			return e, nil
		}
	}
	return dom.Entity{}, dom.ErrNotFound
}

func (s *memoryStore) ListAttempts(_ context.Context, executionID string) ([]dom.Attempt, error) {
	out := make([]dom.Attempt, 0)
	for _, a := range s.attempts {
		if a.ExecutionID == executionID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *memoryStore) List(_ context.Context, f dom.ListFilter) ([]dom.Entity, error) {
	out := make([]dom.Entity, 0, len(s.values))
	for _, e := range s.values {
		if f.Status != "" && e.Status != f.Status {
			continue
		}
		if f.Kind != "" && e.Kind != f.Kind {
			continue
		}
		if f.Runtime != "" && e.Runtime != f.Runtime {
			continue
		}
		if f.TaskID != "" && e.TaskID != f.TaskID {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	start := f.Offset
	if start < 0 {
		start = 0
	}
	if start >= len(out) {
		return []dom.Entity{}, nil
	}
	end := len(out)
	if f.Limit > 0 && start+f.Limit < end {
		end = start + f.Limit
	}
	return out[start:end], nil
}

func (s *memoryStore) RequestCancel(_ context.Context, id string) error {
	s.cancelRequests = append(s.cancelRequests, id)
	return nil
}

func (s *memoryStore) BindRuntimeRef(_ context.Context, id, ref string) (dom.Entity, error) {
	e := s.values[id]
	e.RuntimeRef = ref
	s.values[id] = e
	return e, nil
}

func (s *memoryStore) Transition(_ context.Context, id string, from []dom.LifecycleStatus, to dom.LifecycleStatus, msg string, result []byte) (dom.Entity, error) {
	e := s.values[id]
	allowed := false
	for _, v := range from {
		if e.Status == v {
			allowed = true
		}
	}
	if !allowed {
		return dom.Entity{}, dom.ErrInvalidTransition
	}
	e.Status, e.Error, e.Result = to, msg, result
	s.values[id] = e
	return e, nil
}

func (s *memoryStore) CreateAttempt(_ context.Context, a dom.Attempt) (dom.Attempt, error) {
	if old, ok := s.attempts[a.ID]; ok {
		return old, nil
	}
	s.attempts[a.ID] = a
	return a, nil
}

func (s *memoryStore) FinishAttempt(_ context.Context, id string, status dom.AttemptStatus, ref string, result []byte, message string) (dom.Attempt, error) {
	a, ok := s.attempts[id]
	if !ok {
		return dom.Attempt{}, dom.ErrNotFound
	}
	if a.Status != dom.AttemptRunning {
		return dom.Attempt{}, dom.ErrInvalidTransition
	}
	now := time.Now().UTC()
	a.Status, a.RuntimeRef, a.Result, a.Error, a.FinishedAt, a.UpdatedAt = status, ref, result, message, &now, now
	s.attempts[id] = a
	return a, nil
}

type fakeAdapter struct {
	submitted   bool
	submitCount int
	cancelCount int
	submitErr   error
	cancelErr   error
	observation Observation
}

func (a *fakeAdapter) Submit(context.Context, dom.Entity) (string, error) {
	a.submitted = true
	a.submitCount++
	if a.submitErr != nil {
		return "", a.submitErr
	}
	return "runtime-1", nil
}

func (a *fakeAdapter) Observe(context.Context, dom.Entity) (Observation, error) {
	return a.observation, nil
}

func (a *fakeAdapter) Cancel(context.Context, dom.Entity) error {
	a.cancelCount++
	return a.cancelErr
}

func TestSubmitAndObserve(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	a := &fakeAdapter{observation: Observation{Status: dom.LifecycleRunning, Attempt: 1}}
	if err = m.Register(dom.RuntimeAgent, a); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"prompt": "test"})
	e, err := m.Submit(context.Background(), SubmitRequest{
		Kind: dom.KindAgent, Runtime: dom.RuntimeAgent, TaskID: "task-1",
		Input: input, IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !a.submitted || e.RuntimeRef != "runtime-1" {
		t.Fatalf("adapter not submitted or ref missing: %+v", e)
	}
	got, err := m.Get(context.Background(), e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != dom.LifecycleRunning {
		t.Fatalf("status=%s", got.Status)
	}
	if len(got.Attempts) != 1 || got.Attempts[0].Status != dom.AttemptRunning {
		t.Fatalf("expected one running attempt, got %+v", got.Attempts)
	}
}

func TestGetQueuedDoesNotCreateRunningAttempt(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	a := &fakeAdapter{observation: Observation{Status: dom.LifecycleQueued}}
	if err = m.Register(dom.RuntimeAgent, a); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	e := dom.Entity{
		ID: "execution-queued", Kind: dom.KindAgent, Runtime: dom.RuntimeAgent,
		TaskID: "task-queued", Status: dom.LifecycleQueued,
		Input: json.RawMessage(`{"id":"task-queued"}`), CreatedAt: now, UpdatedAt: now,
	}
	if _, err = store.Create(context.Background(), e); err != nil {
		t.Fatal(err)
	}

	got, err := m.Get(context.Background(), e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != dom.LifecycleQueued {
		t.Fatalf("status=%s, want queued", got.Status)
	}
	if len(got.Attempts) != 0 {
		t.Fatalf("queued execution must not have a running attempt: %+v", got.Attempts)
	}
	if len(store.attempts) != 0 {
		t.Fatalf("queued execution created %d attempts", len(store.attempts))
	}
}

func TestReconcileScansAllPages(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{observation: Observation{Status: dom.LifecycleRunning}}
	if err := m.Register(dom.RuntimeAgent, adapter); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, id := range []string{"exec-a", "exec-b", "exec-c", "exec-d", "exec-e"} {
		_, err := store.Create(context.Background(), dom.Entity{
			ID: id, Kind: dom.KindAgent, Runtime: dom.RuntimeAgent, TaskID: id,
			Status: dom.LifecycleQueued, Input: json.RawMessage(`{"id":"task"}`),
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err := m.Reconcile(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 5 {
		t.Fatalf("scanned=%d, want 5; report=%+v", report.Scanned, report)
	}
	if report.Recovered != 5 {
		t.Fatalf("recovered=%d, want 5; report=%+v", report.Recovered, report)
	}
}

func TestSubmitIdempotencyKeyReusesExecutionAndRejectsChangedPayload(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{observation: Observation{Status: dom.LifecycleQueued}}
	if err := m.Register(dom.RuntimeAgent, adapter); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"id":"task-1","prompt":"same"}`)
	request := SubmitRequest{
		Kind: dom.KindAgent, Runtime: dom.RuntimeAgent, TaskID: "task-1",
		Input: input, IdempotencyKey: "request-key-1",
	}
	first, err := m.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent submit returned different IDs: %s != %s", first.ID, second.ID)
	}
	if adapter.submitCount != 1 {
		t.Fatalf("adapter submissions=%d, want 1", adapter.submitCount)
	}

	request.Input = json.RawMessage(`{"id":"task-1","prompt":"changed"}`)
	if _, err := m.Submit(context.Background(), request); err != dom.ErrConflict {
		t.Fatalf("changed payload error=%v, want ErrConflict", err)
	}
	if adapter.submitCount != 1 {
		t.Fatalf("conflicting request reached adapter: submissions=%d", adapter.submitCount)
	}
}

func TestSubmitDerivesCLIIdentityFromInput(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{observation: Observation{Status: dom.LifecycleQueued}}
	if err := m.Register(dom.RuntimeCLI, adapter); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"id":"cli-task-derived","prompt":"run","workspace_path":"/tmp"}`)
	entity, err := m.Submit(context.Background(), SubmitRequest{
		Kind: dom.KindCLI, Runtime: dom.RuntimeCLI, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if entity.TaskID != "cli-task-derived" {
		t.Fatalf("task ID=%q, want derived CLI request ID", entity.TaskID)
	}
}

func TestResolveNeedsReconciliationRequiresExplicitEvidence(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	entity := dom.Entity{
		ID: "execution-needs-reconcile", Kind: dom.KindAgent, Runtime: dom.RuntimeAgent,
		TaskID: "task-needs-reconcile", Status: dom.LifecycleNeedsReconciliation,
		Input: json.RawMessage(`{"id":"task-needs-reconcile"}`), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.Create(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAttempt(context.Background(), dom.Attempt{
		ID: entity.ID + "-a-1", ExecutionID: entity.ID, Number: 1, Runtime: dom.RuntimeAgent,
		Status: dom.AttemptRunning, Input: entity.Input, StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"verified":true,"source":"operator"}`)
	resolved, err := m.Resolve(context.Background(), entity.ID, dom.LifecycleSucceeded, result, "verified the persisted artifact")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != dom.LifecycleSucceeded || string(resolved.Result) != string(result) {
		t.Fatalf("unexpected resolved execution: %+v", resolved)
	}
	if resolved.Error != "operator resolution: verified the persisted artifact" {
		t.Fatalf("resolution reason was not persisted: %q", resolved.Error)
	}
	if len(resolved.Attempts) != 1 || resolved.Attempts[0].Status != dom.AttemptUnknown {
		t.Fatalf("uncertain runtime attempt must remain unknown: %+v", resolved.Attempts)
	}
}

func TestResolveRejectsUnsafeOrInvalidResolution(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	entity := dom.Entity{
		ID: "execution-running", Kind: dom.KindAgent, Runtime: dom.RuntimeAgent,
		TaskID: "task-running", Status: dom.LifecycleRunning,
		Input: json.RawMessage(`{"id":"task-running"}`), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.Create(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Resolve(context.Background(), entity.ID, dom.LifecycleSucceeded, json.RawMessage(`{"ok":true}`), "confirmed"); err != dom.ErrInvalidTransition {
		t.Fatalf("running resolution error=%v, want invalid transition", err)
	}
	if _, err := m.Resolve(context.Background(), entity.ID, dom.LifecycleSucceeded, nil, "confirmed"); err != ErrInvalidRequest {
		t.Fatalf("missing evidence error=%v, want invalid request", err)
	}
}


func TestCancelPersistsIntentBeforeRuntimeCall(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{cancelErr: errors.New("runtime cancel transport failed")}
	if err := m.Register(dom.RuntimeAgent, adapter); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	entity := dom.Entity{
		ID: "cancel-execution", Kind: dom.KindAgent, Runtime: dom.RuntimeAgent,
		TaskID: "cancel-task", Status: dom.LifecycleRunning,
		Input: json.RawMessage(`{"id":"cancel-task"}`), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.Create(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(context.Background(), entity.ID); err == nil {
		t.Fatal("expected runtime cancellation error")
	}
	if len(store.cancelRequests) != 1 || store.cancelRequests[0] != entity.ID {
		t.Fatalf("cancellation intent was not persisted first: %+v", store.cancelRequests)
	}
	if adapter.cancelCount != 1 {
		t.Fatalf("runtime cancel calls=%d, want 1", adapter.cancelCount)
	}
	current, err := store.Get(context.Background(), entity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != dom.LifecycleRunning {
		t.Fatalf("cancel request must not claim a cancellation outcome: %s", current.Status)
	}
}

func TestSubmitErrorPreservesUncertainOutcome(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{submitErr: errors.New("connection lost after request")}
	if err := m.Register(dom.RuntimeAgent, adapter); err != nil {
		t.Fatal(err)
	}
	entity, submitErr := m.Submit(context.Background(), SubmitRequest{
		Kind: dom.KindAgent, Runtime: dom.RuntimeAgent, TaskID: "uncertain-task",
		Input: json.RawMessage(`{"id":"uncertain-task"}`),
	})
	if submitErr == nil {
		t.Fatal("expected submit error")
	}
	if entity.Status != dom.LifecycleNeedsReconciliation {
		t.Fatalf("status=%s, want needs_reconciliation", entity.Status)
	}
	if entity.Error == "" {
		t.Fatal("uncertain submission reason was not persisted")
	}
}

func TestReconcileSubmitFailureMovesQueuedExecutionToAttention(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{submitErr: errors.New("runtime unavailable")}
	if err := m.Register(dom.RuntimeAgent, adapter); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	entity := dom.Entity{
		ID: "reconcile-uncertain", Kind: dom.KindAgent, Runtime: dom.RuntimeAgent,
		TaskID: "reconcile-task", Status: dom.LifecycleQueued,
		Input: json.RawMessage(`{"id":"reconcile-task"}`), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := store.Create(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
	report, err := m.Reconcile(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 1 || report.NeedsAttention != 1 {
		t.Fatalf("unexpected reconcile report: %+v", report)
	}
	updated, err := store.Get(context.Background(), entity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != dom.LifecycleNeedsReconciliation || updated.Error == "" {
		t.Fatalf("uncertain queued execution not preserved: %+v", updated)
	}
}
