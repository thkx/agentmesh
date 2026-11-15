package reflection

import (
	"context"
	"errors"
	"testing"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	evaldom "github.com/thkx/agentmesh/internal/domain/goalevaluation"
	dom "github.com/thkx/agentmesh/internal/domain/reflection"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
)

type goalsFake struct{ g goaldom.Goal }

func (f goalsFake) Get(context.Context, string) (goaldom.Goal, error) { return f.g, nil }

type runsFake struct{ r wf.Run }

func (f runsFake) GetRun(context.Context, string) (wf.Run, error) { return f.r, nil }

type evalsFake struct {
	e   evaldom.Evaluation
	err error
}

func (f evalsFake) Get(context.Context, string, string) (evaldom.Evaluation, error) {
	return f.e, f.err
}

type reflectorFake struct{ ins []dom.Insight }

func (f reflectorFake) Reflect(context.Context, goaldom.Goal, wf.Run, evaldom.Evaluation, string) (string, []dom.Insight, error) {
	return "Lessons from execution", f.ins, nil
}

type repoFake struct{ item *dom.Reflection }

func (f *repoFake) Get(context.Context, string, string) (dom.Reflection, error) {
	if f.item == nil {
		return dom.Reflection{}, ErrNotFound
	}
	return *f.item, nil
}
func (f *repoFake) List(context.Context, string, int) ([]dom.Reflection, error) { return nil, nil }
func (f *repoFake) Save(_ context.Context, r dom.Reflection) (dom.Reflection, error) {
	f.item = &r
	return r, nil
}
func TestCreateReflectionRequiresEvaluationAndVerifiesEvidence(t *testing.T) {
	goal := goaldom.Goal{ID: "g1", Description: "ship", Status: goaldom.StatusCompleted}
	run := wf.Run{ID: "run1", Status: wf.RunSucceeded, Nodes: map[string]wf.NodeState{"n1": {Status: wf.NodeSucceeded, Output: []byte("all tests passed")}}}
	evaluation := evaldom.Evaluation{ID: "eval1", GoalID: "g1", RunID: "run1", Status: evaldom.StatusSatisfied, Summary: "Goal achieved"}
	ins := []dom.Insight{{Kind: dom.InsightSuccessPattern, Title: "Tests first", Description: "Tests passing confirms the implementation", Confidence: .9, Evidence: []dom.Evidence{{NodeID: "n1", Quote: "all tests passed"}}}}
	repo := &repoFake{}
	svc, err := NewService(goalsFake{goal}, runsFake{run}, evalsFake{e: evaluation}, repo, reflectorFake{ins})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Create(context.Background(), "g1", CreateRequest{RunID: "run1", ModelID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.EvaluationID != "eval1" || len(got.Insights) != 1 {
		t.Fatalf("unexpected reflection: %+v", got)
	}
}
func TestCreateReflectionRejectsInventedEvidence(t *testing.T) {
	run := wf.Run{ID: "run1", Nodes: map[string]wf.NodeState{"n1": {Output: []byte("actual output")}}}
	ins := []dom.Insight{{Kind: dom.InsightRisk, Title: "Risk", Description: "Unsupported claim", Confidence: .5, Evidence: []dom.Evidence{{NodeID: "n1", Quote: "invented"}}}}
	svc, _ := NewService(goalsFake{goaldom.Goal{ID: "g1", Description: "g"}}, runsFake{run}, evalsFake{e: evaldom.Evaluation{ID: "e1"}}, &repoFake{}, reflectorFake{ins})
	if _, err := svc.Create(context.Background(), "g1", CreateRequest{RunID: "run1", ModelID: "m"}); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("expected evidence error, got %v", err)
	}
}
func TestCreateReflectionRequiresPriorEvaluation(t *testing.T) {
	svc, _ := NewService(goalsFake{goaldom.Goal{ID: "g1", Description: "g"}}, runsFake{wf.Run{ID: "run1"}}, evalsFake{err: evaldom.ErrNotFound}, &repoFake{}, reflectorFake{})
	if _, err := svc.Create(context.Background(), "g1", CreateRequest{RunID: "run1", ModelID: "m"}); !errors.Is(err, ErrEvaluationRequired) {
		t.Fatalf("expected evaluation required, got %v", err)
	}
}
func TestExistingReflectionIsIdempotent(t *testing.T) {
	old := dom.Reflection{ID: "old", GoalID: "g1", RunID: "run1", CreatedAt: time.Now()}
	repo := &repoFake{item: &old}
	svc, _ := NewService(goalsFake{}, runsFake{}, evalsFake{}, repo, reflectorFake{})
	got, err := svc.Create(context.Background(), "g1", CreateRequest{RunID: "run1", ModelID: "m"})
	if err != nil || got.ID != "old" {
		t.Fatalf("expected stored reflection, got %+v %v", got, err)
	}
}
