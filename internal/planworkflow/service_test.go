package planworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
)

type goalReaderFake struct{ goal goaldom.Goal }

func (f goalReaderFake) Get(context.Context, string) (goaldom.Goal, error) { return f.goal, nil }

type planReaderFake struct{ plan pland.Plan }

func (f planReaderFake) Get(context.Context, string, int) (pland.Plan, error) { return f.plan, nil }

type repoFake struct {
	record   ExecutionRecord
	reserved bool
}

func (f *repoFake) Reserve(_ context.Context, rec ExecutionRecord) (ExecutionRecord, error) {
	if f.reserved {
		if f.record.InputHash != rec.InputHash {
			return ExecutionRecord{}, ErrIdempotencyConflict
		}
		return f.record, nil
	}
	f.record, f.reserved = rec, true
	return rec, nil
}

type workflowFake struct {
	definition   wf.Definition
	run          wf.Run
	input        map[string]any
	puts, starts int
}

func (f *workflowFake) PutDefinition(_ context.Context, d wf.Definition) error {
	f.definition = d
	f.puts++
	return nil
}
func (f *workflowFake) StartQueued(_ context.Context, workflowID, runID string, input map[string]any, attempts int) (wf.Run, error) {
	f.starts++
	f.input = input
	f.run = wf.Run{ID: runID, WorkflowID: workflowID, Status: wf.RunQueued}
	return f.run, nil
}

func TestStartPlanExecutionIsIdempotent(t *testing.T) {
	goal := goaldom.Goal{ID: "g1", Description: "goal", Status: goaldom.StatusActive}
	plan := pland.Plan{ID: "p1", GoalID: "g1", Version: 1, Objective: "do it", Status: pland.PlanValidated, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Steps: []pland.Step{{ID: "done", Name: "Done", Description: "Done", Type: pland.StepEnd}}}
	repo, engine := &repoFake{}, &workflowFake{}
	svc, err := NewService(goalReaderFake{goal}, planReaderFake{plan}, repo, engine, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	req := StartRequest{Input: map[string]any{"request": "same"}}
	first, err := svc.Start(context.Background(), "g1", 1, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Start(context.Background(), "g1", 1, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Run.ID != second.Run.ID || engine.starts != 2 {
		t.Fatalf("expected deterministic idempotent run, first=%s second=%s starts=%d", first.Run.ID, second.Run.ID, engine.starts)
	}
	if engine.definition.CreatedAt != plan.CreatedAt {
		t.Fatal("compiled workflow timestamp must be stable for immutable definition")
	}
}

func TestStartPlanExecutionRejectsChangedInput(t *testing.T) {
	goal := goaldom.Goal{ID: "g1", Description: "goal", Status: goaldom.StatusActive}
	plan := pland.Plan{ID: "p1", GoalID: "g1", Version: 1, Objective: "do it", Status: pland.PlanValidated, Steps: []pland.Step{{ID: "done", Name: "Done", Description: "Done", Type: pland.StepEnd}}}
	repo, engine := &repoFake{}, &workflowFake{}
	svc, _ := NewService(goalReaderFake{goal}, planReaderFake{plan}, repo, engine, nil, 3)
	if _, err := svc.Start(context.Background(), "g1", 1, StartRequest{Input: map[string]any{"v": 1}}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Start(context.Background(), "g1", 1, StartRequest{Input: map[string]any{"v": 2}})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestRetryBudgetUsesLargestStepBudget(t *testing.T) {
	svc, _ := NewService(goalReaderFake{}, planReaderFake{}, &repoFake{}, &workflowFake{}, nil, 4)
	plan := pland.Plan{Steps: []pland.Step{{RetryPolicy: pland.RetryPolicy{MaxAttempts: 2}}, {RetryPolicy: pland.RetryPolicy{MaxAttempts: 5}}}}
	if got := svc.retryBudget(plan); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
	if got := svc.retryBudget(pland.Plan{}); got != 4 {
		t.Fatalf("expected default 4, got %d", got)
	}
}

type memoryFake struct {
	query, goalID string
	agentIDs      []string
}

func (f *memoryFake) Retrieve(_ context.Context, query, goalID string, agentIDs []string, limit int) (string, error) {
	f.query = query
	f.goalID = goalID
	f.agentIDs = agentIDs
	return "verified memory context", nil
}

func TestStartInjectsRetrievedMemoryIntoRunContext(t *testing.T) {
	goal := goaldom.Goal{ID: "g1", Description: "Build a reliable API", Status: goaldom.StatusActive}
	plan := pland.Plan{ID: "p1", GoalID: "g1", Version: 1, Objective: "Implement retry handling", Status: pland.PlanValidated, Steps: []pland.Step{{ID: "work", Name: "Work", Description: "Implement", Type: pland.StepAgent, AgentProfile: "developer"}, {ID: "done", Name: "Done", Description: "Done", Type: pland.StepEnd, Dependencies: []string{"work"}}}}
	repo, engine, mem := &repoFake{}, &workflowFake{}, &memoryFake{}
	svc, _ := NewService(goalReaderFake{goal}, planReaderFake{plan}, repo, engine, nil, 3)
	svc.SetMemoryRetriever(mem)
	_, err := svc.Start(context.Background(), "g1", 1, StartRequest{Profiles: map[string]AgentProfile{"developer": {ModelID: "model", WorkspaceID: "workspace"}}})
	if err != nil {
		t.Fatal(err)
	}
	byProfile, ok := engine.input["_agentmesh_memory_by_profile"].(map[string]any)
	if !ok || byProfile["developer"] != "verified memory context" {
		t.Fatalf("profile-scoped memory context was not injected: %#v", engine.input)
	}
	if mem.goalID != "g1" || len(mem.agentIDs) != 1 || mem.agentIDs[0] != "developer" {
		t.Fatalf("unexpected retrieval scope: %#v", mem)
	}
}

type snapshotWorkflowFake struct {
	workflowFake
	persisted wf.Run
}

func (f *snapshotWorkflowFake) GetRun(_ context.Context, runID string) (wf.Run, error) {
	if f.persisted.ID == "" {
		return wf.Run{}, wf.ErrNotFound
	}
	if f.persisted.ID != runID {
		return wf.Run{}, wf.ErrNotFound
	}
	return f.persisted, nil
}

func (f *snapshotWorkflowFake) StartQueued(_ context.Context, workflowID, runID string, input map[string]any, _ int) (wf.Run, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return wf.Run{}, err
	}
	f.input = input
	f.starts++
	f.persisted = wf.Run{ID: runID, WorkflowID: workflowID, Status: wf.RunQueued, Input: encoded}
	return f.persisted, nil
}

type changingMemoryFake struct {
	calls int
}

func (f *changingMemoryFake) Retrieve(context.Context, string, string, []string, int) (string, error) {
	f.calls++
	return "memory snapshot v1", nil
}

func TestStartKeepsPersistedMemorySnapshotOnIdempotentRetry(t *testing.T) {
	goal := goaldom.Goal{ID: "snapshot-goal", Description: "Build a reliable API", Status: goaldom.StatusActive}
	plan := pland.Plan{
		ID: "snapshot-plan", GoalID: goal.ID, Version: 1, Objective: "Implement retry handling",
		Status: pland.PlanValidated,
		Steps: []pland.Step{
			{ID: "work", Name: "Work", Description: "Implement retries", Type: pland.StepAgent, AgentProfile: "developer"},
			{ID: "done", Name: "Done", Description: "Done", Type: pland.StepEnd, Dependencies: []string{"work"}},
		},
	}
	repo, engine, mem := &repoFake{}, &snapshotWorkflowFake{}, &changingMemoryFake{}
	svc, err := NewService(goalReaderFake{goal}, planReaderFake{plan}, repo, engine, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetMemoryRetriever(mem)
	req := StartRequest{Profiles: map[string]AgentProfile{"developer": {ModelID: "model", WorkspaceID: "workspace"}}}
	first, err := svc.Start(context.Background(), goal.ID, 1, req)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	firstSnapshot := string(engine.persisted.Input)
	if firstSnapshot == "" {
		t.Fatal("workflow engine did not persist the run input snapshot")
	}

	second, err := svc.Start(context.Background(), goal.ID, 1, req)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if first.Run.ID != second.Run.ID {
		t.Fatalf("expected stable run ID, first=%s second=%s", first.Run.ID, second.Run.ID)
	}
	if mem.calls != 1 {
		t.Fatalf("memory must be retrieved once for an immutable run snapshot, got %d calls", mem.calls)
	}
	if string(engine.persisted.Input) != firstSnapshot {
		t.Fatalf("idempotent retry changed persisted memory snapshot: first=%s second=%s", firstSnapshot, string(engine.persisted.Input))
	}
	byProfile, ok := engine.input["_agentmesh_memory_by_profile"].(map[string]any)
	if !ok || byProfile["developer"] != "memory snapshot v1" {
		t.Fatalf("persisted profile memory was not restored: %#v", engine.input)
	}
}
