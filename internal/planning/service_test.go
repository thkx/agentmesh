package planning

import (
	"context"
	"testing"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
)

type fakeGoalReader struct {
	goal goaldom.Goal
	err  error
}

func (f fakeGoalReader) Get(context.Context, string) (goaldom.Goal, error) { return f.goal, f.err }

type fakePlanner struct {
	draft Draft
	err   error
}

func (f fakePlanner) Generate(context.Context, goaldom.Goal, string) (Draft, error) {
	return f.draft, f.err
}

type fakePlanRepository struct{ plan pland.Plan }

func (f *fakePlanRepository) Create(_ context.Context, p pland.Plan) (pland.Plan, error) {
	p.Version = 1
	f.plan = p
	return p, nil
}
func (f *fakePlanRepository) Get(context.Context, string, int) (pland.Plan, error) {
	return f.plan, nil
}
func (f *fakePlanRepository) List(context.Context, string, int) ([]pland.Plan, error) {
	return []pland.Plan{f.plan}, nil
}

func TestPlanningServiceGeneratesValidatedPlan(t *testing.T) {
	goals := fakeGoalReader{goal: goaldom.Goal{ID: "g1", Description: "Research", Status: goaldom.StatusActive}}
	planner := fakePlanner{draft: Draft{Objective: "Complete research", Steps: []pland.Step{{ID: "research", Name: "Research", Description: "Collect sources", Type: pland.StepAgent, AgentProfile: "research"}}}}
	repo := &fakePlanRepository{}
	service, err := NewService(goals, planner, repo)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Generate(context.Background(), "g1", "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != 1 || plan.Status != pland.PlanValidated || plan.GoalID != "g1" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestPlanningServiceRejectsInvalidDraft(t *testing.T) {
	goals := fakeGoalReader{goal: goaldom.Goal{ID: "g1", Description: "Research", Status: goaldom.StatusActive}}
	planner := fakePlanner{draft: Draft{Objective: "invalid", Steps: []pland.Step{{ID: "bad id", Name: "Bad", Description: "Bad", Type: pland.StepAgent, AgentProfile: "research"}}}}
	service, _ := NewService(goals, planner, &fakePlanRepository{})
	if _, err := service.Generate(context.Background(), "g1", "model-1"); err == nil {
		t.Fatal("expected invalid draft to fail")
	}
}
