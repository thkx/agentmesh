package collaboration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thkx/agentmesh/internal/collaboration"
)

func TestBuildTaskResultEvaluationContextIncludesBoundedArtifactEvidence(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Evaluation Context", Slug: "evaluation-context"})
	if err != nil { t.Fatal(err) }
	agent, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Reviewer", Role: "reviewer"})
	if err != nil { t.Fatal(err) }
	task, err := service.CreateTask(ctx, collaboration.Task{WorkspaceID: workspace.ID, Title: "Produce a tested deliverable"})
	if err != nil { t.Fatal(err) }
	assignment, err := service.AssignTask(ctx, task.ID, agent.ID, "human")
	if err != nil { t.Fatal(err) }
	content := []byte("verified deliverable")
	artifact, version, err := service.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		WorkspaceID: workspace.ID, SourceTaskID: task.ID, CreatorAgentID: agent.ID,
		Kind: "document", Title: "Implementation report", Content: content, MediaType: "text/plain",
	})
	if err != nil { t.Fatal(err) }
	if _, err = service.AddArtifactEvidence(ctx, artifact.ID, collaboration.AddArtifactEvidenceRequest{
		VersionID: version.ID, Kind: "test", ContentSHA256: version.ContentSHA256,
		Statement: "unit tests passed", CreatedBy: agent.ID,
	}); err != nil { t.Fatal(err) }
	if _, err = service.VerifyArtifact(ctx, artifact.ID, 1, collaboration.ArtifactVerificationPolicy{
		RequiredEvidenceKinds: []string{"test"}, RequireContentDigest: true,
	}); err != nil { t.Fatal(err) }
	result, _ := json.Marshal(map[string]string{"summary": "completed"})
	if err = service.StoreTaskResult(ctx, agent.ID, task.ID, assignment.ID, "execution-context-test", result); err != nil { t.Fatal(err) }

	evaluation, err := service.BuildTaskResultEvaluationContext(ctx, agent.ID, assignment.ID)
	if err != nil { t.Fatal(err) }
	if evaluation.Task.ID != task.ID || evaluation.Result.AssignmentID != assignment.ID {
		t.Fatalf("task/result mismatch: %+v", evaluation)
	}
	if len(evaluation.Artifacts) != 1 { t.Fatalf("expected one linked artifact, got %d", len(evaluation.Artifacts)) }
	got := evaluation.Artifacts[0]
	if !got.ContentIncluded || string(got.Version.Content) != string(content) {
		t.Fatalf("artifact content missing from evaluation context: %+v", got)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Kind != "test" {
		t.Fatalf("artifact evidence missing: %+v", got.Evidence)
	}
	if len(got.Verifications) != 1 || got.Verifications[0].Outcome != "passed" {
		t.Fatalf("artifact verification missing: %+v", got.Verifications)
	}
}

func TestBuildTaskResultEvaluationContextDoesNotCrossWorkspace(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	first, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "First", Slug: "first"})
	if err != nil { t.Fatal(err) }
	second, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Second", Slug: "second"})
	if err != nil { t.Fatal(err) }
	agent, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: first.ID, Name: "Worker", Role: "worker"})
	if err != nil { t.Fatal(err) }
	task, err := service.CreateTask(ctx, collaboration.Task{WorkspaceID: first.ID, Title: "Private task"})
	if err != nil { t.Fatal(err) }
	assignment, err := service.AssignTask(ctx, task.ID, agent.ID, "human")
	if err != nil { t.Fatal(err) }
	if _, _, err = service.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		WorkspaceID: second.ID, Kind: "document", Title: "Other workspace", Content: []byte("secret"),
	}); err != nil { t.Fatal(err) }
	result := json.RawMessage(`{"ok":true}`)
	if err = service.StoreTaskResult(ctx, agent.ID, task.ID, assignment.ID, "execution-isolated", result); err != nil { t.Fatal(err) }
	evaluation, err := service.BuildTaskResultEvaluationContext(ctx, agent.ID, assignment.ID)
	if err != nil { t.Fatal(err) }
	if len(evaluation.Artifacts) != 0 { t.Fatalf("cross-workspace artifact leaked: %+v", evaluation.Artifacts) }
}
