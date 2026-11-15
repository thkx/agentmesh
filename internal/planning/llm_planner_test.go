package planning

import (
	"context"
	"testing"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

type fakeModel struct {
	response *modelgateway.GenerateResponse
	err      error
	request  modelgateway.GenerateRequest
}

func (f *fakeModel) Generate(_ context.Context, req modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	f.request = req
	return f.response, f.err
}

func TestLLMPlannerParsesStructuredPlan(t *testing.T) {
	model := &fakeModel{response: &modelgateway.GenerateResponse{Content: `{"objective":"Research and report","steps":[{"id":"research","name":"Research","description":"Collect sources","type":"agent","agent_profile":"research"}]}`}}
	planner, err := NewLLMPlanner(model, 2048)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := planner.Generate(context.Background(), goaldom.Goal{ID: "g1", Description: "Research a product"}, "model-1")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Objective != "Research and report" || len(draft.Steps) != 1 {
		t.Fatalf("unexpected draft: %+v", draft)
	}
	if model.request.ModelID != "model-1" || model.request.MaxOutputTokens != 2048 {
		t.Fatalf("unexpected model request: %+v", model.request)
	}
}

func TestLLMPlannerRejectsUnknownFields(t *testing.T) {
	model := &fakeModel{response: &modelgateway.GenerateResponse{Content: `{"objective":"x","steps":[],"execute_shell":"rm -rf /"}`}}
	planner, _ := NewLLMPlanner(model, 1000)
	if _, err := planner.Generate(context.Background(), goaldom.Goal{ID: "g1", Description: "x"}, "model-1"); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}
