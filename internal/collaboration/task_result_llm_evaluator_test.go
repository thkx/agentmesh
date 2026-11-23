package collaboration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thkx/agentmesh/internal/collaboration"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

type fixedEvaluationModel struct {
	content  string
	response *modelgateway.GenerateResponse
	request  modelgateway.GenerateRequest
}

func (m *fixedEvaluationModel) Generate(_ context.Context, request modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	m.request = request
	if m.response != nil {
		response := *m.response
		// Preserve the fixture's decision content while allowing tests to
		// provide response provenance and usage metadata independently.
		if response.Content == "" {
			response.Content = m.content
		}
		return &response, nil
	}
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
		Result:    collaboration.TaskResult{TaskID: "task-1", RecipientAgentID: "agent-1", Result: json.RawMessage(`{"summary":"done"}`)},
		Artifacts: []collaboration.TaskArtifactContext{{
			Artifact:        collaboration.Artifact{ID: "artifact-1", Status: "submitted", SubmittedVersionID: "version-1"},
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
	if model.request.ModelID != "reviewer-model" || model.request.MaxOutputTokens != 1024 {
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
			Artifact:      collaboration.Artifact{Status: "submitted", SubmittedVersionID: "version-1"},
			Version:       collaboration.ArtifactVersion{ID: "version-1"},
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
		Workspace: collaboration.Workspace{AutonomyMode: "hybrid"},
		Result:    collaboration.TaskResult{RecipientAgentID: "agent-1"},
		Artifacts: []collaboration.TaskArtifactContext{{
			Artifact:      collaboration.Artifact{Status: "submitted", SubmittedVersionID: "version-1"},
			Version:       collaboration.ArtifactVersion{ID: "version-1"},
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

func TestLLMTaskResultEvaluatorCannotAcceptRejectedArtifactEvidence(t *testing.T) {
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(&fixedEvaluationModel{
		content: `{"outcome":"accepted","reason":"Looks good.","confidence":0.99,"evidence_refs":["evidence-old","verification-old"]}`,
	}, func(context.Context, string) (string, error) { return "reviewer-model", nil })
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := evaluator.EvaluateTaskResultWithContext(context.Background(), collaboration.TaskResultEvaluationContext{
		Workspace: collaboration.Workspace{AutonomyMode: "hybrid"},
		Result:    collaboration.TaskResult{RecipientAgentID: "agent-1"},
		Artifacts: []collaboration.TaskArtifactContext{{
			Artifact:      collaboration.Artifact{ID: "old-artifact", Status: "rejected", SubmittedVersionID: "version-old"},
			Version:       collaboration.ArtifactVersion{ID: "version-old"},
			Evidence:      []collaboration.ArtifactEvidence{{ID: "evidence-old"}},
			Verifications: []collaboration.ArtifactVerification{{ID: "verification-old", Outcome: "passed"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "human_review" {
		t.Fatalf("rejected Artifact evidence must not support acceptance, got %+v", assessment)
	}
}

func TestLLMTaskResultEvaluatorRequiresSubmittedExactArtifactVersion(t *testing.T) {
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(&fixedEvaluationModel{
		content: `{"outcome":"accepted","reason":"Looks good.","confidence":0.99,"evidence_refs":["evidence-draft","verification-draft"]}`,
	}, func(context.Context, string) (string, error) { return "reviewer-model", nil })
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := evaluator.EvaluateTaskResultWithContext(context.Background(), collaboration.TaskResultEvaluationContext{
		Workspace: collaboration.Workspace{AutonomyMode: "hybrid"},
		Result:    collaboration.TaskResult{RecipientAgentID: "agent-1"},
		Artifacts: []collaboration.TaskArtifactContext{{
			Artifact:      collaboration.Artifact{ID: "draft-artifact", Status: "draft"},
			Version:       collaboration.ArtifactVersion{ID: "version-draft"},
			Evidence:      []collaboration.ArtifactEvidence{{ID: "evidence-draft"}},
			Verifications: []collaboration.ArtifactVerification{{ID: "verification-draft", Outcome: "passed"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "human_review" {
		t.Fatalf("draft Artifact evidence must not support acceptance, got %+v", assessment)
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

func TestLLMTaskResultEvaluatorUsesLatestVerificationOnly(t *testing.T) {
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(&fixedEvaluationModel{
		content: `{"outcome":"accepted","reason":"Looks good.","confidence":0.99,"evidence_refs":["evidence-1","verification-old"]}`,
	}, func(context.Context, string) (string, error) { return "reviewer-model", nil })
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := evaluator.EvaluateTaskResultWithContext(context.Background(), collaboration.TaskResultEvaluationContext{
		Workspace: collaboration.Workspace{AutonomyMode: "hybrid"},
		Result:    collaboration.TaskResult{RecipientAgentID: "agent-1"},
		Artifacts: []collaboration.TaskArtifactContext{{
			Artifact: collaboration.Artifact{ID: "artifact-1", Status: "submitted", SubmittedVersionID: "version-1"},
			Version:  collaboration.ArtifactVersion{ID: "version-1"},
			Evidence: []collaboration.ArtifactEvidence{{ID: "evidence-1"}},
			Verifications: []collaboration.ArtifactVerification{
				{ID: "verification-new", Outcome: "failed"},
				{ID: "verification-old", Outcome: "passed"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "human_review" {
		t.Fatalf("an older passed verification must not override the latest failed verification, got %+v", assessment)
	}
}


func TestLLMTaskResultEvaluatorAcceptsGroundedPlainTextResult(t *testing.T) {
	model := &fixedEvaluationModel{
		content: `{"outcome":"accepted","reason":"The result addresses each requested point.","confidence":0.96,"evidence_refs":["task:task-plain","result:result-plain"]}`,
		response: &modelgateway.GenerateResponse{
			ID: "response-plain-review", ProviderID: provider.ID("provider-deepseek"),
			ModelID: "deepseek-reviewer-model",
			Usage: modelgateway.Usage{InputTokens: 321, OutputTokens: 87, TotalTokens: 408},
		},
	}
	resolvedAgentID := ""
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(model, func(_ context.Context, agentID string) (string, error) {
		resolvedAgentID = agentID
		return "deepseek-reviewer-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	assessment, err := evaluator.EvaluateTaskResultWithContext(context.Background(), collaboration.TaskResultEvaluationContext{
		Workspace: collaboration.Workspace{AutonomyMode: "hybrid"},
		Task: collaboration.Task{
			ID: "task-plain", Title: "Explain webhook idempotency",
			Description: "Explain duplicate delivery, idempotency keys, and safe retries.",
		},
		Result: collaboration.TaskResult{
			ID: "result-plain", TaskID: "task-plain", RecipientAgentID: "reviewer-agent",
			Result: json.RawMessage(`{"content":"The result covers duplicate delivery, unique keys, and safe retries."}`),
		},
		Artifacts: []collaboration.TaskArtifactContext{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "accepted" || assessment.Confidence != 0.96 {
		t.Fatalf("expected accepted plain-text result, got %+v", assessment)
	}
	if resolvedAgentID != "reviewer-agent" || model.request.ModelID != "deepseek-reviewer-model" {
		t.Fatalf("reviewer must use the recipient Agent's pinned model: agent=%q model=%q", resolvedAgentID, model.request.ModelID)
	}
	if string(assessment.Evidence) != `["task:task-plain","result:result-plain"]` {
		t.Fatalf("plain-result references were not persisted: %s", assessment.Evidence)
	}
	if assessment.ReviewMethod != "model" || !assessment.ModelInvoked ||
		assessment.ReviewerModelID != "deepseek-reviewer-model" ||
		assessment.ReviewerProviderID != "provider-deepseek" ||
		assessment.ModelResponseID != "response-plain-review" ||
		assessment.InputTokens != 321 || assessment.OutputTokens != 87 {
		t.Fatalf("review model provenance/usage was not captured: %+v", assessment)
	}
	if assessment.ReviewMethod != "model" || !assessment.ModelInvoked || assessment.ReviewerModelID != "deepseek-reviewer-model" {
		t.Fatalf("review model provenance missing: %+v", assessment)
	}
}


func TestLLMTaskResultEvaluatorTreatsLengthFinishReasonAsTruncated(t *testing.T) {
	model := &fixedEvaluationModel{
		content: `{"outcome":"accepted","reason":"The result covers all requirements.","confidence":0.99,"evidence_refs":["task:task-plain","result:result-plain"]}`,
		response: &modelgateway.GenerateResponse{
			ID: "response-truncated-review",
			ProviderID: provider.ID("provider-deepseek"),
			ModelID: "deepseek-reviewer-model",
			FinishReason: modelgateway.FinishReasonLength,
			Usage: modelgateway.Usage{InputTokens: 400, OutputTokens: 1024, TotalTokens: 1424},
		},
	}
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(model, func(context.Context, string) (string, error) {
		return "deepseek-reviewer-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	assessment, err := evaluator.EvaluateTaskResultWithContext(context.Background(), collaboration.TaskResultEvaluationContext{
		Workspace: collaboration.Workspace{AutonomyMode: "hybrid"},
		Task: collaboration.Task{ID: "task-plain", Description: "Complete all requirements."},
		Result: collaboration.TaskResult{
			ID: "result-plain", RecipientAgentID: "reviewer-agent",
			Result: json.RawMessage(`{"content":"complete"}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "human_review" {
		t.Fatalf("length-truncated response must require human review, got %+v", assessment)
	}
	if assessment.Reason != "reviewer output was truncated (finish_reason=length); human review is required" {
		t.Fatalf("unexpected truncation reason: %q", assessment.Reason)
	}
	if assessment.ReviewMethod != "model" || !assessment.ModelInvoked ||
		assessment.ModelResponseID != "response-truncated-review" ||
		assessment.OutputTokens != 1024 {
		t.Fatalf("model provenance must be preserved for truncated output: %+v", assessment)
	}
}

func TestLLMTaskResultEvaluatorRejectsInventedPlainResultReferences(t *testing.T) {
	model := &fixedEvaluationModel{content: `{"outcome":"accepted","reason":"Looks complete.","confidence":0.99,"evidence_refs":["task:task-plain","result:invented"]}`}
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(model, func(context.Context, string) (string, error) {
		return "reviewer-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := evaluator.EvaluateTaskResultWithContext(context.Background(), collaboration.TaskResultEvaluationContext{
		Workspace: collaboration.Workspace{AutonomyMode: "hybrid"},
		Task:      collaboration.Task{ID: "task-plain", Description: "Provide a concise explanation."},
		Result:    collaboration.TaskResult{ID: "result-plain", RecipientAgentID: "reviewer-agent", Result: json.RawMessage(`{"content":"done"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "human_review" {
		t.Fatalf("invented result reference must require human review, got %+v", assessment)
	}
}

func TestLLMTaskResultEvaluatorRequiresBothPlainResultReferences(t *testing.T) {
	model := &fixedEvaluationModel{content: `{"outcome":"rejected","reason":"The result omits required details.","confidence":0.98,"evidence_refs":["result:result-plain"]}`}
	evaluator, err := collaboration.NewLLMTaskResultEvaluator(model, func(context.Context, string) (string, error) {
		return "reviewer-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := evaluator.EvaluateTaskResultWithContext(context.Background(), collaboration.TaskResultEvaluationContext{
		Workspace: collaboration.Workspace{AutonomyMode: "hybrid"},
		Task:      collaboration.Task{ID: "task-plain", Description: "Provide all required details."},
		Result:    collaboration.TaskResult{ID: "result-plain", RecipientAgentID: "reviewer-agent", Result: json.RawMessage(`{"content":"partial"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Outcome != "human_review" {
		t.Fatalf("missing task reference must require human review, got %+v", assessment)
	}
}
