package manager

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/coding"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

type fakeCodingAdapterService struct {
	task           coding.Task
	getErr         error
	startCalls     int
	boundExecution string
	boundAttempt   string
}

func (f *fakeCodingAdapterService) ValidateCreateRequest(_ context.Context, _ coding.CreateRequest) error {
	return f.validateErr
}
func (f *fakeCodingAdapterService) Create(_ context.Context, in coding.CreateRequest) (coding.Task, error) {
	f.getErr = nil
	f.task = coding.Task{ID: in.ID, ExecutionID: in.ExecutionID, AttemptID: in.AttemptID, Status: coding.StatusPending}
	return f.task, nil
}
func (f *fakeCodingAdapterService) Get(_ context.Context, _ string) (coding.Task, error) {
	if f.getErr != nil {
		return coding.Task{}, f.getErr
	}
	return f.task, nil
}
func (f *fakeCodingAdapterService) Start(_ context.Context, _ string) (coding.Task, error) {
	f.startCalls++
	return f.task, nil
}
func (f *fakeCodingAdapterService) Cancel(string) error { return nil }
func (f *fakeCodingAdapterService) BindExecution(_ context.Context, _ string, executionID, attemptID string) error {
	f.boundExecution = executionID
	f.boundAttempt = attemptID
	f.task.ExecutionID = executionID
	f.task.AttemptID = attemptID
	return nil
}

func TestCodingAdapterSubmitBindsCanonicalAttemptAndIsIdempotent(t *testing.T) {
	service := &fakeCodingAdapterService{getErr: coding.ErrNotFound}
	adapter, err := NewCodingAdapter(service)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(coding.CreateRequest{ID: "coding-task-1", RepositoryPath: "/tmp/repo", ModelID: "model", Prompt: "change"})
	entity := dom.Entity{ID: "execution-1", Kind: dom.KindCoding, Runtime: dom.RuntimeCoding, TaskID: "coding-task-1", Input: input}
	ref, err := adapter.Submit(context.Background(), entity)
	if err != nil {
		t.Fatal(err)
	}
	if ref != "coding-task-1" || service.startCalls != 1 {
		t.Fatalf("ref=%q starts=%d", ref, service.startCalls)
	}
	if service.boundExecution != "execution-1" || service.boundAttempt != "execution-1-a-1" {
		t.Fatalf("provenance not bound: execution=%q attempt=%q", service.boundExecution, service.boundAttempt)
	}
	service.task.Status = coding.StatusRunning
	if _, err = adapter.Submit(context.Background(), entity); err != nil {
		t.Fatalf("repeated submit should be idempotent: %v", err)
	}
	if service.startCalls != 1 {
		t.Fatalf("repeated submit started task again: %d", service.startCalls)
	}
}

func TestCodingAdapterMapsInterruptedOutcome(t *testing.T) {
	service := &fakeCodingAdapterService{task: coding.Task{ID: "coding-task-2", Status: coding.StatusInterrupted, OutcomeUnknown: true, Error: "process restarted"}}
	adapter, err := NewCodingAdapter(service)
	if err != nil {
		t.Fatal(err)
	}
	got, err := adapter.Observe(context.Background(), dom.Entity{ID: "execution-2", RuntimeRef: "coding-task-2"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != dom.LifecycleInterrupted || got.AttemptOutcome != "" {
		t.Fatalf("unexpected observation: %+v", got)
	}
}

type fakeCodingAttemptStore struct{ attempts []dom.Attempt }

func (f *fakeCodingAttemptStore) ListAttempts(_ context.Context, _ string) ([]dom.Attempt, error) {
	return append([]dom.Attempt(nil), f.attempts...), nil
}
func (f *fakeCodingAttemptStore) CreateAttempt(_ context.Context, a dom.Attempt) (dom.Attempt, error) {
	f.attempts = append(f.attempts, a)
	return a, nil
}

func TestCodingAdapterEnsuresTerminalAttemptBeforeArtifactFinalization(t *testing.T) {
	service := &fakeCodingAdapterService{task: coding.Task{
		ID: "coding-task-3", ExecutionID: "execution-3", AttemptID: "execution-3-a-1",
		Status: coding.StatusSucceeded, StartedAt: time.Now().UTC(),
	}}
	attempts := &fakeCodingAttemptStore{}
	adapter, err := NewCodingAdapter(service, attempts)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := adapter.Observe(context.Background(), dom.Entity{
		ID: "execution-3", Runtime: dom.RuntimeCoding, RuntimeRef: "coding-task-3",
		Input: json.RawMessage(`{"id":"coding-task-3"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status != dom.LifecycleSucceeded || len(attempts.attempts) != 1 {
		t.Fatalf("observation=%+v attempts=%+v", observed, attempts.attempts)
	}
	attempt := attempts.attempts[0]
	if attempt.ID != "execution-3-a-1" || attempt.Status != dom.AttemptRunning || attempt.RuntimeRef != "coding-task-3" {
		t.Fatalf("unexpected pre-finalization attempt: %+v", attempt)
	}
}

func TestCodingAdapterReschedulesPendingTaskAfterRestart(t *testing.T) {
	service := &fakeCodingAdapterService{task: coding.Task{ID: "coding-pending", Status: coding.StatusPending}}
	adapter, err := NewCodingAdapter(service)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := adapter.Observe(context.Background(), dom.Entity{ID: "execution-pending", RuntimeRef: "coding-pending"})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status != dom.LifecycleQueued || service.startCalls != 1 {
		t.Fatalf("observation=%+v start_calls=%d", observed, service.startCalls)
	}
}

func TestCodingAdapterDoesNotCreateAttemptForPreStartCancellation(t *testing.T) {
	service := &fakeCodingAdapterService{task: coding.Task{
		ID: "coding-canceled", Status: coding.StatusCanceled,
	}}
	attempts := &fakeCodingAttemptStore{}
	adapter, err := NewCodingAdapter(service, attempts)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := adapter.Observe(context.Background(), dom.Entity{ID: "execution-canceled", RuntimeRef: "coding-canceled"})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status != dom.LifecycleCanceled || !observed.AttemptNotStarted || len(attempts.attempts) != 0 {
		t.Fatalf("observation=%+v attempts=%+v", observed, attempts.attempts)
	}
}

func TestCodingAdapterRejectsDisallowedRepositoryBeforeSubmit(t *testing.T) {
	service := &fakeCodingAdapterService{validateErr: coding.ErrRepositoryNotAllowed}
	adapter, err := NewCodingAdapter(service)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(coding.CreateRequest{ID: "coding-task-denied", RepositoryPath: "/private", ModelID: "model", Prompt: "change"})
	err = adapter.ValidateSubmit(context.Background(), SubmitRequest{TaskID: "coding-task-denied", Input: input})
	if !errors.Is(err, coding.ErrRepositoryNotAllowed) {
		t.Fatalf("validation error=%v", err)
	}
	if service.startCalls != 0 || service.task.ID != "" {
		t.Fatal("rejected coding task reached persistence or execution")
	}
}
