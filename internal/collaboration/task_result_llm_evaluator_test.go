package collaboration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thkx/agentmesh/internal/collaboration"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

type fixedEvaluationModel struct {
	content string
	request modelgateway.GenerateRequest
}

func (m *fixedEvaluationModel) Generate(_ context.Context, request modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	m.request = request
	return &modelgateway.GenerateResponse{Content: m.content}, nil
}

func TestLLMTaskResultEvaluatorRequiresGroundedEvidence(t *testing.T) {
	ctx := context.Background()
	evidenceID := "evidence-test"
	verificationID := "verification-test"
	model := &fixedEvaluationModel{content: `{"outcome":"accepted","reason":"The deliverable passed its checks.","confidence":0.97,"evidence_refs":["evidence-test","verification-test"]}`}
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(model, func(context.Context, string) (string, error) {
		return "reviewer-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := evaluator.EvaluateTaskResultWithContext(ctx, collaboration.TaskResultEvaluationContext{
		Workspace: collaboration.Workspace{AutonomyMode: "hybrid"},
		Task:      collaboration.Task{ID: "task-1", Title: "Deliver tested work", Description: "Provide implementation and tests."},
		Result: collaboration.TaskResult{TaskID: "task-1", RecipientAgentID: "agent-1", Result: json.RawMessage(`{"summary":"done"}`)},
		Artifacts: []collaboration.TaskArtifactContext{{
			Artifact:        collaboration.Artifact{ID: "artifact-1"},
			Version:         collaboration.ArtifactVersion{ID: "version-1", ContentSHA256: "digest"},
			ContentIncluded: true,
			Evidence:        []collaboration.ArtifactEvidence{{ID: evidenceID, Kind: "test"}},
			Verifications:   []collaboration.ArtifactVerification{{ID: verificationID, Outcome: "passed"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "accepted" {
		t.Fatalf("expected grounded acceptance, got %+v", assessment)
	}
	if model.request.ModelID != "reviewer-model" || model.request.MaxOutputTokens != 512 {
		t.Fatalf("unexpected model request: %+v", model.request)
	}
}

func TestLLMTaskResultEvaluatorRejectsInventedEvidence(t *testing.T) {
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(&fixedEvaluationModel{
		content: `{"outcome":"accepted","reason":"Looks good.","confidence":0.99,"evidence_refs":["invented"]}`,
	}, func(context.Context, string) (string, error) { return "reviewer-model", nil })
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := evaluator.EvaluateTaskResultWithContext(context.Background(), collaboration.TaskResultEvaluationContext{
		Workspace: collaboration.Workspace{AutonomyMode: "hybrid"},
		Result:    collaboration.TaskResult{RecipientAgentID: "agent-1"},
		Artifacts: []collaboration.TaskArtifactContext{{
			Evidence:      []collaboration.ArtifactEvidence{{ID: "real-evidence"}},
			Verifications: []collaboration.ArtifactVerification{{ID: "real-verification", Outcome: "passed"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "human_review" {
		t.Fatalf("invented evidence must require human review, got %+v", assessment)
	}
}

func TestLLMTaskResultEvaluatorRequiresPassedArtifactVerificationForAcceptance(t *testing.T) {
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(&fixedEvaluationModel{
		content: `{"outcome":"accepted","reason":"Looks good.","confidence":0.99,"evidence_refs":["evidence-1"]}`,
	}, func(context.Context, string) (string, error) { return "reviewer-model", nil })
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := evaluator.EvaluateTaskResultWithContext(context.Background(), collaboration.TaskResultEvaluationContext{
		Result: collaboration.TaskResult{RecipientAgentID: "agent-1"},
		Artifacts: []collaboration.TaskArtifactContext{{
			Evidence:      []collaboration.ArtifactEvidence{{ID: "evidence-1"}},
			Verifications: []collaboration.ArtifactVerification{{ID: "verification-1", Outcome: "needs_evidence"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "human_review" {
		t.Fatalf("unverified artifact must require human review, got %+v", assessment)
	}
}

func TestLLMTaskResultEvaluatorHonorsDirectedWorkspaceMode(t *testing.T) {
	model := &fixedEvaluationModel{content: `{"outcome":"accepted","reason":"Looks good.","confidence":0.99,"evidence_refs":["evidence-1","verification-1"]}`}
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(model, func(context.Context, string) (string, error) {
		return "reviewer-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := evaluator.EvaluateTaskResultWithContext(context.Background(), collaboration.TaskResultEvaluationContext{
		Workspace: collaboration.Workspace{AutonomyMode: "directed"},
		Result:    collaboration.TaskResult{RecipientAgentID: "agent-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "human_review" {
		t.Fatalf("directed workspace must require human review, got %+v", assessment)
	}
	if model.request.ModelID != "" {
		t.Fatal("directed workspace must not invoke the model reviewer")
	}
}
