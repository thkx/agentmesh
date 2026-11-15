package collaboration_test

import (
	"context"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/collaboration"
)

func TestArtifactExecutionAttemptProvenanceIsQueryableAndIdempotent(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Provenance", Slug: "provenance"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixNano()
	for _, id := range []string{"exec-source", "exec-other"} {
		_, err = db.DB.ExecContext(ctx, `INSERT INTO executions
			(id,kind,runtime,status,error_text,created_at,updated_at)
			VALUES(?, 'agent','agent','succeeded','',?,?)`, id, now, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		id, executionID string
	}{{"attempt-source", "exec-source"}, {"attempt-other", "exec-other"}} {
		_, err = db.DB.ExecContext(ctx, `INSERT INTO execution_attempts
			(id,execution_id,attempt_number,runtime,status,error_text,started_at,finished_at,created_at,updated_at)
			VALUES(?,?,1,'agent','succeeded','',?,?,?,?)`,
			item.id, item.executionID, now, now, now, now)
		if err != nil {
			t.Fatal(err)
		}
	}

	artifact, version, err := service.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		WorkspaceID: workspace.ID, Kind: "report", Title: "Execution report",
		Content: []byte("verified output"), SourceExecution: "exec-source",
	})
	if err != nil {
		t.Fatal(err)
	}
	bind := collaboration.BindArtifactVersionAttemptRequest{ExecutionID: "exec-source", AttemptID: "attempt-source"}
	if err = service.BindArtifactVersionAttempt(ctx, artifact.ID, version.Version, bind); err != nil {
		t.Fatal(err)
	}
	// Repeating the same binding is safe; binding a different Attempt is not.
	if err = service.BindArtifactVersionAttempt(ctx, artifact.ID, version.Version, bind); err != nil {
		t.Fatalf("idempotent provenance bind: %v", err)
	}
	if err = service.BindArtifactVersionAttempt(ctx, artifact.ID, version.Version,
		collaboration.BindArtifactVersionAttemptRequest{ExecutionID: "exec-other", AttemptID: "attempt-other"}); err != collaboration.ErrConflict {
		t.Fatalf("expected conflicting provenance bind, got %v", err)
	}

	evidence, err := service.CreateArtifactEvidenceWithProvenance(ctx, artifact.ID,
		collaboration.CreateArtifactEvidenceProvenanceRequest{
			VersionID: version.ID, Kind: "test", SourceRef: "go test ./...",
			ExecutionID: "exec-source", AttemptID: "attempt-source",
			Statement: "Tests passed for this attempt",
		})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.ExecutionID != "exec-source" || evidence.AttemptID != "attempt-source" {
		t.Fatalf("unexpected evidence provenance: %+v", evidence)
	}

	if _, err = service.CreateArtifactEvidenceWithProvenance(ctx, artifact.ID,
		collaboration.CreateArtifactEvidenceProvenanceRequest{
			VersionID: version.ID, Kind: "build", ExecutionID: "exec-source", AttemptID: "attempt-other",
		}); err != collaboration.ErrConflict {
		t.Fatalf("expected attempt/execution mismatch, got %v", err)
	}

	got, err := service.GetArtifactProvenance(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Versions) != 1 || got.Versions[0].SourceExecutionID != "exec-source" ||
		got.Versions[0].SourceAttemptID != "attempt-source" {
		t.Fatalf("unexpected version provenance: %+v", got.Versions)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].ExecutionID != "exec-source" ||
		got.Evidence[0].AttemptID != "attempt-source" {
		t.Fatalf("unexpected evidence provenance: %+v", got.Evidence)
	}
}
