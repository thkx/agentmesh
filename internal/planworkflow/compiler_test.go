package planworkflow

import (
	"context"
	"testing"
	"time"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
)

type allowProfiles struct{}

func (allowProfiles) ValidateProfile(context.Context, string, AgentProfile) error { return nil }

func testGoalAndPlan(steps []pland.Step) (goaldom.Goal, pland.Plan) {
	goal := goaldom.Goal{ID: "goal-1", Description: "test goal", Status: goaldom.StatusActive}
	plan := pland.Plan{ID: "plan-1", GoalID: goal.ID, Version: 1, Objective: "complete test", Status: pland.PlanValidated, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Steps: steps}
	return goal, plan
}

func TestCompileLinearPlan(t *testing.T) {
	goal, plan := testGoalAndPlan([]pland.Step{
		{ID: "build", Name: "Build", Description: "Build the feature", Type: pland.StepAgent, AgentProfile: "developer"},
		{ID: "finish", Name: "Finish", Description: "Finish", Type: pland.StepEnd, Dependencies: []string{"build"}},
	})
	definition, err := Compile(context.Background(), goal, plan, CompileOptions{Profiles: map[string]AgentProfile{"developer": {ModelID: "model-1", WorkspaceID: "workspace-1"}}, Validator: allowProfiles{}})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Entry != "step-build" || len(definition.Nodes) != 2 || len(definition.Edges) != 1 {
		t.Fatalf("unexpected compiled definition: %+v", definition)
	}
	if definition.Nodes[0].Agent == nil || definition.Nodes[0].Agent.ModelID != "model-1" {
		t.Fatalf("agent profile was not resolved: %+v", definition.Nodes[0])
	}
}

func TestCompileConditionBranches(t *testing.T) {
	goal, plan := testGoalAndPlan([]pland.Step{
		{ID: "check", Name: "Check", Description: "Check result", Type: pland.StepCondition, Input: map[string]any{"input_key": "approved", "equals": true, "true_next": "yes", "false_next": "no"}},
		{ID: "yes", Name: "Yes", Description: "Approved", Type: pland.StepEnd, Dependencies: []string{"check"}},
		{ID: "no", Name: "No", Description: "Not approved", Type: pland.StepEnd, Dependencies: []string{"check"}},
	})
	definition, err := Compile(context.Background(), goal, plan, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.Edges) != 2 {
		t.Fatalf("expected two condition edges, got %d", len(definition.Edges))
	}
}

func TestCompileMultipleRootsAddsSyntheticEntry(t *testing.T) {
	goal, plan := testGoalAndPlan([]pland.Step{
		{ID: "left", Name: "Left", Description: "Left branch", Type: pland.StepEnd},
		{ID: "right", Name: "Right", Description: "Right branch", Type: pland.StepEnd},
	})
	definition, err := Compile(context.Background(), goal, plan, CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Entry != "__plan_start" || len(definition.Nodes) != 3 || len(definition.Edges) != 2 {
		t.Fatalf("unexpected synthetic entry: %+v", definition)
	}
}

func TestCompileRejectsMissingAgentProfile(t *testing.T) {
	goal, plan := testGoalAndPlan([]pland.Step{{ID: "work", Name: "Work", Description: "Do work", Type: pland.StepAgent, AgentProfile: "missing"}})
	if _, err := Compile(context.Background(), goal, plan, CompileOptions{}); err == nil {
		t.Fatal("expected missing profile binding error")
	}
}
