package goalevaluation

import (
	"context"
	"errors"
	"testing"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	evaldom "github.com/thkx/agentmesh/internal/domain/goalevaluation"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
)

type goalReaderFake struct{ goal goaldom.Goal }

func (f goalReaderFake) Get(context.Context, string) (goaldom.Goal, error) { return f.goal, nil }

type runReaderFake struct{ run wf.Run }

func (f runReaderFake) GetRun(context.Context, string) (wf.Run, error) { return f.run, nil }

type evaluatorFake struct{ results []evaldom.CriterionResult }

func (f evaluatorFake) Evaluate(context.Context, goaldom.Goal, wf.Run, string) (string, []evaldom.CriterionResult, error) {
	return "Verified outcome", f.results, nil
}

type repositoryFake struct {
	existing *evaldom.Evaluation
	saved    evaldom.Evaluation
	complete bool
}

func (f *repositoryFake) FindExecution(context.Context, string, string) (ExecutionLink, error) {
	return ExecutionLink{GoalID: "g1", RunID: "r1"}, nil
}
func (f *repositoryFake) Get(context.Context, string, string) (evaldom.Evaluation, error) {
	if f.existing == nil {
		return evaldom.Evaluation{}, ErrNotFound
	}
	return *f.existing, nil
}
func (f *repositoryFake) List(context.Context, string, int) ([]evaldom.Evaluation, error) {
	return nil, nil
}
func (f *repositoryFake) Save(_ context.Context, e evaldom.Evaluation, complete bool) (evaldom.Evaluation, error) {
	f.saved = e
	f.complete = complete
	f.existing = &e
	return e, nil
}

func TestEvaluateCompletesGoalOnlyWithVerifiedRequiredEvidence(t *testing.T) {
	goal := goaldom.Goal{ID: "g1", Description: "ship feature", Status: goaldom.StatusActive, SuccessCriteria: []goaldom.SuccessCriterion{{ID: "tests", Description: "Tests pass", Required: true}, {ID: "docs", Description: "Docs updated", Required: false}}}
	run := wf.Run{ID: "r1", Status: wf.RunSucceeded, Nodes: map[string]wf.NodeState{"step-tests": {Status: wf.NodeSucceeded, Output: []byte(`{"result":"all tests passed"}`)}}}
	results := []evaldom.CriterionResult{{CriterionID: "tests", Status: evaldom.StatusSatisfied, Reason: "Test output confirms success", Evidence: []evaldom.Evidence{{NodeID: "step-tests", Quote: "all tests passed"}}}, {CriterionID: "docs", Status: evaldom.StatusInconclusive, Reason: "No documentation evidence"}}
	repo := &repositoryFake{}
	svc, err := NewService(goalReaderFake{goal}, runReaderFake{run}, repo, evaluatorFake{results})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Evaluate(context.Background(), "g1", EvaluateRequest{RunID: "r1", ModelID: "model-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != evaldom.StatusSatisfied || !repo.complete {
		t.Fatalf("goal should complete: status=%s complete=%t", got.Status, repo.complete)
	}
}

func TestEvaluateRejectsInventedEvidence(t *testing.T) {
	goal := goaldom.Goal{ID: "g1", Description: "goal", Status: goaldom.StatusActive, SuccessCriteria: []goaldom.SuccessCriterion{{ID: "c1", Description: "criterion", Required: true}}}
	run := wf.Run{ID: "r1", Status: wf.RunSucceeded, Nodes: map[string]wf.NodeState{"n1": {Status: wf.NodeSucceeded, Output: []byte(`{"ok":true}`)}}}
	result := []evaldom.CriterionResult{{CriterionID: "c1", Status: evaldom.StatusSatisfied, Reason: "looks good", Evidence: []evaldom.Evidence{{NodeID: "n1", Quote: "invented evidence"}}}}
	svc, _ := NewService(goalReaderFake{goal}, runReaderFake{run}, &repositoryFake{}, evaluatorFake{result})
	if _, err := svc.Evaluate(context.Background(), "g1", EvaluateRequest{RunID: "r1", ModelID: "m"}); !errors.Is(err, ErrInvalidEvaluationResult) {
		t.Fatalf("expected invalid evidence, got %v", err)
	}
}

func TestFailedWorkflowCannotCompleteGoal(t *testing.T) {
	goal := goaldom.Goal{ID: "g1", Description: "goal", Status: goaldom.StatusActive, SuccessCriteria: []goaldom.SuccessCriterion{{ID: "c1", Description: "criterion", Required: true}}}
	run := wf.Run{ID: "r1", Status: wf.RunFailed, Nodes: map[string]wf.NodeState{"n1": {Status: wf.NodeSucceeded, Output: []byte(`{"ok":true}`)}}}
	result := []evaldom.CriterionResult{{CriterionID: "c1", Status: evaldom.StatusSatisfied, Reason: "output says done", Evidence: []evaldom.Evidence{{NodeID: "n1", Quote: `"ok":true`}}}}
	repo := &repositoryFake{}
	svc, _ := NewService(goalReaderFake{goal}, runReaderFake{run}, repo, evaluatorFake{result})
	got, err := svc.Evaluate(context.Background(), "g1", EvaluateRequest{RunID: "r1", ModelID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != evaldom.StatusInconclusive || repo.complete {
		t.Fatalf("failed workflow must not complete goal: %+v", got)
	}
}

func TestEvaluateRequiresTerminalRun(t *testing.T) {
	goal := goaldom.Goal{ID: "g1", Description: "goal", Status: goaldom.StatusActive, SuccessCriteria: []goaldom.SuccessCriterion{{ID: "c1", Description: "criterion", Required: true}}}
	svc, _ := NewService(goalReaderFake{goal}, runReaderFake{wf.Run{ID: "r1", Status: wf.RunRunning}}, &repositoryFake{}, evaluatorFake{})
	_, err := svc.Evaluate(context.Background(), "g1", EvaluateRequest{RunID: "r1", ModelID: "m"})
	if !errors.Is(err, ErrRunNotTerminal) {
		t.Fatalf("expected terminal error, got %v", err)
	}
}

func TestExistingEvaluationIsReturnedWithoutReevaluation(t *testing.T) {
	old := evaldom.Evaluation{ID: "e1", GoalID: "g1", RunID: "r1", ModelID: "m", Summary: "done", Status: evaldom.StatusSatisfied, Criteria: []evaldom.CriterionResult{{CriterionID: "c1", Status: evaldom.StatusSatisfied, Reason: "ok"}}, CreatedAt: time.Now()}
	repo := &repositoryFake{existing: &old}
	svc, _ := NewService(goalReaderFake{}, runReaderFake{}, repo, evaluatorFake{})
	got, err := svc.Evaluate(context.Background(), "g1", EvaluateRequest{RunID: "r1", ModelID: "m"})
	if err != nil || got.ID != "e1" {
		t.Fatalf("expected existing result, got %+v %v", got, err)
	}
}
