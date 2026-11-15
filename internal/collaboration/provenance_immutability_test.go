package collaboration_test

import (
	"context"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/collaboration"
)

func TestArtifactProvenanceBindingPreservesVersionContentImmutability(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{
		Name: "Immutable provenance", Slug: "immutable-provenance",
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().UnixNano()
	if _, err = db.DB.ExecContext(ctx, `INSERT INTO executions
		(id,kind,runtime,status,error_text,created_at,updated_at)
		VALUES('immutable-exec','agent','agent','succeeded','',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.ExecContext(ctx, `INSERT INTO execution_attempts
		(id,execution_id,attempt_number,runtime,status,error_text,started_at,finished_at,created_at,updated_at)
		VALUES('immutable-attempt','immutable-exec',1,'agent','succeeded','',?,?,?,?)`,
		now, now, now, now); err != nil {
		t.Fatal(err)
	}

	artifact, version, err := service.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		WorkspaceID: workspace.ID,
		Kind: "report",
		Title: "Immutable report",
		Content: []byte("original artifact bytes"),
		SourceExecution: "immutable-exec",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err = service.BindArtifactVersionAttempt(ctx, artifact.ID, version.Version,
		collaboration.BindArtifactVersionAttemptRequest{
			ExecutionID: "immutable-exec",
			AttemptID:   "immutable-attempt",
		}); err != nil {
		t.Fatalf("one-time provenance binding must be allowed: %v", err)
	}

	if _, err = db.DB.ExecContext(ctx, `UPDATE collaboration_artifact_versions
		SET content=? WHERE id=?`, []byte("tampered bytes"), version.ID); err == nil {
		t.Fatal("artifact content mutation must remain forbidden after provenance binding")
	}
	if _, err = db.DB.ExecContext(ctx, `UPDATE collaboration_artifact_versions
		SET metadata_json=? WHERE id=?`, `{"tampered":true}`, version.ID); err == nil {
		t.Fatal("artifact metadata mutation must remain forbidden after provenance binding")
	}

	var sourceExecution, sourceAttempt string
	if err = db.DB.QueryRowContext(ctx, `SELECT source_execution_id,source_attempt_id
		FROM collaboration_artifact_versions WHERE id=?`, version.ID).
		Scan(&sourceExecution, &sourceAttempt); err != nil {
		t.Fatal(err)
	}
	if sourceExecution != "immutable-exec" || sourceAttempt != "immutable-attempt" {
		t.Fatalf("unexpected immutable provenance: execution=%q attempt=%q", sourceExecution, sourceAttempt)
	}
}
