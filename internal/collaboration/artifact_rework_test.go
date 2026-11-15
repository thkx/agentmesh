package collaboration_test

import (
	"context"
	"testing"

	"github.com/thkx/agentmesh/internal/collaboration"
)

func TestRejectedArtifactBlocksSourceTaskAndAllowsReassignment(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Artifact Rework", Slug: "artifact-rework"})
	if err != nil {
		t.Fatal(err)
	}
	parentAgent, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Lead", Role: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	firstWorker, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Author", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	secondWorker, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Rework Author", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, collaboration.Task{
		WorkspaceID: workspace.ID, Title: "Deliver reviewed artifact", CreatedBy: parentAgent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AssignTask(ctx, task.ID, firstWorker.ID, parentAgent.ID); err != nil {
		t.Fatal(err)
	}

	artifact, version, err := service.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		WorkspaceID: workspace.ID, SourceTaskID: task.ID, CreatorAgentID: firstWorker.ID,
		Kind: "document", Title: "Draft implementation report", Content: []byte("initial draft"), MediaType: "text/plain",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AddArtifactEvidence(ctx, artifact.ID, collaboration.AddArtifactEvidenceRequest{
		VersionID: version.ID, Kind: "test", ContentSHA256: version.ContentSHA256,
		Statement: "initial test run completed", CreatedBy: firstWorker.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.VerifyArtifact(ctx, artifact.ID, 1, collaboration.ArtifactVerificationPolicy{
		RequiredEvidenceKinds: []string{"test"}, RequireContentDigest: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SubmitArtifact(ctx, artifact.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ReviewArtifact(ctx, artifact.ID, parentAgent.ID, "rejected", "The report is missing the rollback procedure."); err != nil {
		t.Fatal(err)
	}

	blocked, err := service.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != "blocked" {
		t.Fatalf("rejected artifact should block source task, got %q", blocked.Status)
	}
	reassignment, err := service.AssignTask(ctx, task.ID, secondWorker.ID, parentAgent.ID)
	if err != nil {
		t.Fatalf("blocked task should accept a new Agent assignment: %v", err)
	}
	if reassignment.AgentID != secondWorker.ID || reassignment.Status != "pending" {
		t.Fatalf("unexpected reassignment: %+v", reassignment)
	}

	reopened, err := service.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != "assigned" {
		t.Fatalf("task should be assigned after rework handoff, got %q", reopened.Status)
	}
}
