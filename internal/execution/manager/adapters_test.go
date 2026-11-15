package manager

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/agent/scheduler"
	agentstate "github.com/thkx/agentmesh/internal/agent/state"
	"github.com/thkx/agentmesh/internal/coding"
	agentdom "github.com/thkx/agentmesh/internal/domain/agent"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
)

type fakeCodingService struct {
	task     coding.Task
	getErr   error
	starts   int
	canceled string
}

func (s *fakeCodingService) Create(_ context.Context, in coding.CreateRequest) (coding.Task, error) {
	s.getErr = nil
	s.task = coding.Task{ID: in.ID, Status: coding.StatusPending}
	return s.task, nil
}
func (s *fakeCodingService) Get(_ context.Context, id string) (coding.Task, error) {
	if s.getErr != nil {
		return coding.Task{}, s.getErr
	}
	if s.task.ID != id {
		return coding.Task{}, coding.ErrNotFound
	}
	return s.task, nil
}
func (s *fakeCodingService) Start(_ context.Context, id string) (coding.Task, error) {
	s.starts++
	s.task.Status = coding.StatusRunning
	return s.task, nil
}
func (s *fakeCodingService) Cancel(id string) error { s.canceled = id; return nil }

func TestCodingAdapterSubmitIsIdempotentAcrossExistingTask(t *testing.T) {
	service := &fakeCodingService{getErr: coding.ErrNotFound}
	adapter, err := NewCodingAdapter(service)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"id":"coding-1","repository_path":"/tmp/repo","model_id":"model-1","prompt":"implement"}`)
	entity := dom.Entity{ID: "exec-1", TaskID: "coding-1", Input: input}
	for i := 0; i < 2; i++ {
		ref, err := adapter.Submit(context.Background(), entity)
		if err != nil {
			t.Fatal(err)
		}
		if ref != "coding-1" {
			t.Fatalf("ref=%q", ref)
		}
	}
	if service.starts != 1 {
		t.Fatalf("starts=%d, want 1", service.starts)
	}
	if err := adapter.Cancel(context.Background(), dom.Entity{RuntimeRef: "coding-1"}); err != nil {
		t.Fatal(err)
	}
	if service.canceled != "coding-1" {
		t.Fatalf("canceled=%q", service.canceled)
	}
}

type fakeWorkflowEngine struct {
	runs     map[string]wf.Run
	getErr   error
	starts   int
	canceled string
}

func (e *fakeWorkflowEngine) StartQueued(_ context.Context, workflowID, runID string, input map[string]any, _ int) (wf.Run, error) {
	e.starts++
	run := wf.Run{ID: runID, WorkflowID: workflowID, Status: wf.RunQueued, Context: input}
	e.runs[runID] = run
	return run, nil
}
func (e *fakeWorkflowEngine) GetRun(_ context.Context, id string) (wf.Run, error) {
	if e.getErr != nil {
		return wf.Run{}, e.getErr
	}
	run, ok := e.runs[id]
	if !ok {
		return wf.Run{}, wf.ErrNotFound
	}
	return run, nil
}
func (e *fakeWorkflowEngine) CancelRun(_ context.Context, id string) error {
	e.canceled = id
	run := e.runs[id]
	run.Status = wf.RunCanceled
	e.runs[id] = run
	return nil
}

func TestWorkflowAdapterReusesExistingRun(t *testing.T) {
	engine := &fakeWorkflowEngine{runs: map[string]wf.Run{}}
	adapter, err := NewWorkflowAdapter(engine, 3)
	if err != nil {
		t.Fatal(err)
	}
	entity := dom.Entity{ID: "exec-wf-1", TaskID: "run-1", Input: []byte(`{"workflow_id":"wf-1","input":{"goal":"test"}}`)}
	for i := 0; i < 2; i++ {
		ref, err := adapter.Submit(context.Background(), entity)
		if err != nil {
			t.Fatal(err)
		}
		if ref != "run-1" {
			t.Fatalf("ref=%q", ref)
		}
	}
	if engine.starts != 1 {
		t.Fatalf("starts=%d, want 1", engine.starts)
	}
	if err := adapter.Cancel(context.Background(), dom.Entity{RuntimeRef: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if engine.canceled != "run-1" {
		t.Fatalf("canceled=%q", engine.canceled)
	}
}

func TestWorkflowAdapterDoesNotMaskStoreFailure(t *testing.T) {
	engine := &fakeWorkflowEngine{runs: map[string]wf.Run{}, getErr: errors.New("database unavailable")}
	adapter, err := NewWorkflowAdapter(engine, 3)
	if err != nil {
		t.Fatal(err)
	}
	entity := dom.Entity{ID: "exec-wf-2", TaskID: "run-2", Input: []byte(`{"workflow_id":"wf-1","input":{}}`)}
	if _, err := adapter.Submit(context.Background(), entity); err == nil {
		t.Fatal("expected lookup error")
	}
	if engine.starts != 0 {
		t.Fatalf("unexpected StartQueued calls: %d", engine.starts)
	}
}

func TestWorkflowAdapterMapsWaitingApprovalToRunning(t *testing.T) {
	engine := &fakeWorkflowEngine{runs: map[string]wf.Run{"run-3": {ID: "run-3", WorkflowID: "wf-1", Status: wf.RunWaitingApproval}}}
	adapter, err := NewWorkflowAdapter(engine, 3)
	if err != nil {
		t.Fatal(err)
	}
	got, err := adapter.Observe(context.Background(), dom.Entity{RuntimeRef: "run-3"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != dom.LifecycleRunning {
		t.Fatalf("status=%s, want running", got.Status)
	}
}

type fakeAgentScheduler struct {
	job          scheduler.Job
	enqueueCalls int
	canceledTask string
}

func (s *fakeAgentScheduler) Enqueue(_ context.Context, task agentdom.Task, jobID string, maxAttempts int) (scheduler.Job, error) {
	s.enqueueCalls++
	if s.job.ID == "" {
		s.job = scheduler.Job{ID: jobID, Task: task, Status: scheduler.Queued, MaxAttempts: maxAttempts}
	}
	return s.job, nil
}
func (s *fakeAgentScheduler) Get(_ context.Context, id string) (scheduler.Job, error) {
	if s.job.ID == "" || s.job.ID != id {
		return scheduler.Job{}, scheduler.ErrNotFound
	}
	return s.job, nil
}
func (s *fakeAgentScheduler) CancelTask(_ context.Context, id string) error {
	s.canceledTask = id
	s.job.Status = scheduler.Canceled
	return nil
}

type fakeAgentRuntime struct {
	snapshot     agentstate.Snapshot
	canceledTask string
}

func (r *fakeAgentRuntime) Cancel(id string) error { r.canceledTask = id; return nil }
func (r *fakeAgentRuntime) GetSnapshot(context.Context, string) (agentstate.Snapshot, error) {
	return r.snapshot, nil
}

func TestAgentAdapterPersistsStableTaskIdentityAndMapsSuccess(t *testing.T) {
	queue := &fakeAgentScheduler{}
	runtime := &fakeAgentRuntime{}
	adapter, err := NewAgentAdapter(queue, runtime, 4)
	if err != nil {
		t.Fatal(err)
	}
	task := agentdom.Task{ID: "agent-task-1", ModelID: "model-1", WorkspaceID: "workspace-1", Prompt: "do work"}
	input, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	entity := dom.Entity{ID: "exec-agent-1", TaskID: task.ID, Input: input}
	ref, err := adapter.Submit(context.Background(), entity)
	if err != nil {
		t.Fatal(err)
	}
	if ref != task.ID || queue.job.ID != task.ID || queue.job.MaxAttempts != 4 {
		t.Fatalf("unexpected scheduler enqueue: ref=%q job=%+v", ref, queue.job)
	}
	queue.job.Status = scheduler.Succeeded
	runtime.snapshot = agentstate.Snapshot{Task: task, Result: agentdom.Result{TaskID: task.ID, Status: agentdom.StatusSucceeded, Content: "done"}}
	observed, err := adapter.Observe(context.Background(), dom.Entity{RuntimeRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status != dom.LifecycleSucceeded {
		t.Fatalf("status=%s", observed.Status)
	}
	var result agentdom.Result
	if err := json.Unmarshal(observed.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" {
		t.Fatalf("result=%+v", result)
	}
}

func TestAgentAdapterCancellationUsesRuntimeForRunningAndQueueForQueued(t *testing.T) {
	queue := &fakeAgentScheduler{job: scheduler.Job{ID: "job-1", Task: agentdom.Task{ID: "task-1"}, Status: scheduler.Queued}}
	runtime := &fakeAgentRuntime{}
	adapter, err := NewAgentAdapter(queue, runtime, 2)
	if err != nil {
		t.Fatal(err)
	}
	entity := dom.Entity{RuntimeRef: "job-1"}
	if err := adapter.Cancel(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
	if queue.canceledTask != "task-1" {
		t.Fatalf("queued cancel target=%q", queue.canceledTask)
	}
	queue.job.Status = scheduler.Running
	if err := adapter.Cancel(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
	if runtime.canceledTask != "task-1" {
		t.Fatalf("running cancel target=%q", runtime.canceledTask)
	}
}


func TestAgentAdapterKeepsExecutionRunningBetweenSafeRetries(t *testing.T) {
	queue := &fakeAgentScheduler{job: scheduler.Job{
		ID: "agent-retry-job", Task: agentdom.Task{ID: "agent-retry-task"},
		Status: scheduler.Queued, Attempts: 1, MaxAttempts: 3,
		ExecutionStarted: true, LastError: "transient model failure",
	}}
	adapter, err := NewAgentAdapter(queue, &fakeAgentRuntime{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := adapter.Observe(context.Background(), dom.Entity{
		ID: "agent-retry-execution", RuntimeRef: queue.job.ID, Status: dom.LifecycleRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status != dom.LifecycleRunning || observed.CompletedAttempt != 1 ||
		observed.AttemptOutcome != dom.AttemptFailed || !observed.AttemptNotStarted {
		t.Fatalf("retry wait observation=%+v", observed)
	}

	// A claimed retry has not started until the scheduler crosses MarkStarted.
	queue.job.Status = scheduler.Running
	queue.job.Attempts = 2
	queue.job.ExecutionStarted = false
	observed, err = adapter.Observe(context.Background(), dom.Entity{
		ID: "agent-retry-execution", RuntimeRef: queue.job.ID, Status: dom.LifecycleRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status != dom.LifecycleRunning || observed.CompletedAttempt != 1 ||
		observed.AttemptOutcome != dom.AttemptFailed || !observed.AttemptNotStarted {
		t.Fatalf("claimed retry observation=%+v", observed)
	}

	queue.job.ExecutionStarted = true
	observed, err = adapter.Observe(context.Background(), dom.Entity{
		ID: "agent-retry-execution", RuntimeRef: queue.job.ID, Status: dom.LifecycleRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Status != dom.LifecycleRunning || observed.Attempt != 2 || observed.AttemptNotStarted {
		t.Fatalf("started retry observation=%+v", observed)
	}
}

func TestAgentObserverKeepsParentExecutionRunningAcrossRetries(t *testing.T) {
	store := newMemoryStore()
	now := time.Now().UTC()
	task := agentdom.Task{ID: "agent-observer-task", ModelID: "model-1", WorkspaceID: "workspace-1", Prompt: "retry"}
	input, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	entity := dom.Entity{
		ID: "agent-observer-execution", Kind: dom.KindAgent, Runtime: dom.RuntimeAgent,
		TaskID: task.ID, Status: dom.LifecycleQueued, Input: input, CreatedAt: now, UpdatedAt: now,
	}
	if _, err = store.Create(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
	observer, err := NewAgentObserver(store)
	if err != nil {
		t.Fatal(err)
	}
	job := scheduler.Job{ID: task.ID, Task: task, Status: scheduler.Running, Attempts: 1}
	if err = observer.Started(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	job.Status = scheduler.Queued
	if err = observer.Finished(context.Background(), job, agentdom.Result{TaskID: task.ID, Status: agentdom.StatusFailed}, errors.New("safe retry")); err != nil {
		t.Fatal(err)
	}
	current, err := store.Get(context.Background(), entity.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := store.ListAttempts(context.Background(), entity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != dom.LifecycleRunning || len(attempts) != 1 || attempts[0].Status != dom.AttemptFailed {
		t.Fatalf("retry transition corrupted parent lifecycle: execution=%+v attempts=%+v", current, attempts)
	}

	job.Status = scheduler.Running
	job.Attempts = 2
	if err = observer.Started(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	attempts, err = store.ListAttempts(context.Background(), entity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[1].Number != 2 || attempts[1].Status != dom.AttemptRunning {
		t.Fatalf("next retry attempt was not appended: %+v", attempts)
	}
}
