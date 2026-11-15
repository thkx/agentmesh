package planning

import "testing"

func validPlan() Plan {
	return Plan{ID: "p1", GoalID: "g1", Objective: "complete research", Steps: []Step{
		{ID: "research", Name: "Research", Description: "Collect relevant sources", Type: StepAgent, AgentProfile: "research"},
		{ID: "write", Name: "Write", Description: "Write the report", Type: StepAgent, AgentProfile: "writer", Dependencies: []string{"research"}},
	}}
}

func TestPlanValidate(t *testing.T) {
	if err := validPlan().Validate(); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
}

func TestPlanValidateRejectsCycle(t *testing.T) {
	p := validPlan()
	p.Steps[0].Dependencies = []string{"write"}
	if err := p.Validate(); err == nil {
		t.Fatal("expected dependency cycle to fail")
	}
}

func TestPlanValidateRejectsUnknownDependency(t *testing.T) {
	p := validPlan()
	p.Steps[1].Dependencies = []string{"missing"}
	if err := p.Validate(); err == nil {
		t.Fatal("expected unknown dependency to fail")
	}
}
