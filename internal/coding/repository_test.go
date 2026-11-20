package coding

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func testRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	schema := `CREATE TABLE coding_tasks (
      id TEXT PRIMARY KEY,title TEXT NOT NULL,repository_path TEXT NOT NULL,model_id TEXT NOT NULL,prompt TEXT NOT NULL,status TEXT NOT NULL,
      workspace_id TEXT NOT NULL DEFAULT '',workspace_path TEXT NOT NULL DEFAULT '',branch_name TEXT NOT NULL DEFAULT '',test_command_json TEXT NOT NULL DEFAULT '[]',
      max_repair_attempts INTEGER NOT NULL DEFAULT 2,repair_attempts INTEGER NOT NULL DEFAULT 0,timeout_seconds INTEGER NOT NULL DEFAULT 1800,
      result_json TEXT NOT NULL DEFAULT '{}',error TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,started_at INTEGER NOT NULL DEFAULT 0,finished_at INTEGER NOT NULL DEFAULT 0,execution_id TEXT NOT NULL DEFAULT '',outcome_unknown INTEGER NOT NULL DEFAULT 0,attempt_id TEXT NOT NULL DEFAULT '');
      CREATE TABLE coding_task_events(id TEXT PRIMARY KEY,task_id TEXT NOT NULL,event_type TEXT NOT NULL,payload_json TEXT NOT NULL,created_at INTEGER NOT NULL);
      CREATE TABLE coding_task_steps(id TEXT PRIMARY KEY,task_id TEXT NOT NULL,sequence INTEGER NOT NULL,kind TEXT NOT NULL,status TEXT NOT NULL,input_json TEXT NOT NULL,output_json TEXT NOT NULL,error TEXT NOT NULL,started_at INTEGER NOT NULL,finished_at INTEGER NOT NULL,exit_code INTEGER NOT NULL DEFAULT 0,duration_ms INTEGER NOT NULL DEFAULT 0,UNIQUE(task_id,sequence));
      CREATE TABLE coding_task_snapshots(id TEXT PRIMARY KEY,task_id TEXT NOT NULL,label TEXT NOT NULL,commit_hash TEXT NOT NULL,diff TEXT NOT NULL,created_at INTEGER NOT NULL,execution_id TEXT NOT NULL DEFAULT '',attempt_id TEXT NOT NULL DEFAULT '',content_sha256 TEXT NOT NULL DEFAULT '');`
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	r, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return r
}
func TestRepositoryCreateGetAndClaim(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	created, err := r.Create(ctx, CreateRequest{ID: "task-1", RepositoryPath: "/tmp/repo", ModelID: "model", Prompt: "change"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := r.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != StatusPending || loaded.MaxRepairAttempts != 2 || loaded.TimeoutSeconds != 1800 {
		t.Fatalf("unexpected task: %#v", loaded)
	}
	ok, err := r.Claim(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	ok, err = r.Claim(ctx, created.ID)
	if err != nil || ok {
		t.Fatalf("second claim should fail: ok=%v err=%v", ok, err)
	}
	loaded, err = r.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != StatusRunning {
		t.Fatalf("status = %s", loaded.Status)
	}
}

func TestRepositoryPersistsStepOutputAsText(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	_, err := r.Create(ctx, CreateRequest{ID: "task-step", RepositoryPath: "/tmp/repo", ModelID: "model", Prompt: "change"})
	if err != nil {
		t.Fatal(err)
	}
	err = r.AddStep(ctx, Step{TaskID: "task-step", Sequence: 1, Kind: "agent", Status: "succeeded", Output: "created file"})
	if err != nil {
		t.Fatal(err)
	}
	steps, err := r.Steps(ctx, "task-step")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Output != "created file" {
		t.Fatalf("unexpected steps: %#v", steps)
	}
}

func TestRepositoryCreateClaimAndUpdateEventsAreAtomic(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	task, err := r.Create(ctx, CreateRequest{ID: "events-1", RepositoryPath: "/tmp/repo", ModelID: "model", Prompt: "change"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := r.Events(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "task.created" {
		t.Fatalf("create event missing: %#v", events)
	}
	claimed, err := r.ClaimWithEvent(ctx, task.ID, "task.started", nil)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	task, err = r.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = StatusSucceeded
	if err = r.UpdateWithEvent(ctx, task, "task.succeeded", map[string]bool{"ok": true}); err != nil {
		t.Fatal(err)
	}
	events, err = r.Events(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].Type != "task.started" || events[2].Type != "task.succeeded" {
		t.Fatalf("unexpected lifecycle events: %#v", events)
	}
}

func TestRepositoryCancelPendingIsAtomicAndPreventsClaim(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	task, err := r.Create(ctx, CreateRequest{ID: "cancel-pending", RepositoryPath: "/tmp/repo", ModelID: "model", Prompt: "change"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CancelPending(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := r.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != StatusCanceled {
		t.Fatalf("status=%s, want canceled", loaded.Status)
	}
	claimed, err := r.ClaimWithEvent(ctx, task.ID, "task.started", nil)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("canceled pending task must not be claimable")
	}
	events, err := r.Events(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Type != "task.canceled" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestRepositoryCancelPendingRejectsMissingOrClaimedTask(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	if err := r.CancelPending(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("missing task error=%v, want ErrNotFound", err)
	}
	task, err := r.Create(ctx, CreateRequest{ID: "already-claimed", RepositoryPath: "/tmp/repo", ModelID: "model", Prompt: "change"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := r.Claim(ctx, task.ID)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if err := r.CancelPending(ctx, task.ID); err != ErrConflict {
		t.Fatalf("claimed task error=%v, want ErrConflict", err)
	}
}

func TestRecoverInterruptedPreservesUnknownOutcomeAndEvent(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	task, err := r.Create(ctx, CreateRequest{ID: "restart-unknown", ExecutionID: "execution-1", RepositoryPath: "/tmp/repo", ModelID: "model", Prompt: "change"})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.BindExecution(ctx, task.ID, "execution-1", "execution-1-a-1"); err != nil {
		t.Fatal(err)
	}
	if err = r.BindExecution(ctx, task.ID, "execution-1", "execution-1-a-1"); err != nil {
		t.Fatalf("idempotent bind: %v", err)
	}
	if err = r.BindExecution(ctx, task.ID, "execution-2", "execution-2-a-1"); err != ErrConflict {
		t.Fatalf("conflicting bind error=%v", err)
	}
	if claimed, err := r.ClaimWithEvent(ctx, task.ID, "task.started", nil); err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	task, err = r.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = StatusTesting
	if err = r.Update(ctx, task); err != nil {
		t.Fatal(err)
	}
	count, err := r.RecoverInterrupted(ctx)
	if err != nil || count != 1 {
		t.Fatalf("recovered=%d err=%v", count, err)
	}
	got, err := r.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusInterrupted || !got.OutcomeUnknown || got.ExecutionID != "execution-1" || got.AttemptID != "execution-1-a-1" {
		t.Fatalf("unexpected recovered task: %+v", got)
	}
	events, err := r.Events(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[2].Type != "task.interrupted" {
		t.Fatalf("interruption event missing: %+v", events)
	}
	count, err = r.RecoverInterrupted(ctx)
	if err != nil || count != 0 {
		t.Fatalf("repeated recovery should be idempotent: count=%d err=%v", count, err)
	}
}

func TestCodingSnapshotPersistsExecutionAttemptAndDigest(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	_, err := r.Create(ctx, CreateRequest{ID: "snapshot-task", RepositoryPath: "/tmp/repo", ModelID: "model", Prompt: "change"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{
		TaskID: "snapshot-task", Label: "final", CommitHash: "abc123", Diff: "diff --git a/a b/a",
		ExecutionID: "execution-snapshot", AttemptID: "execution-snapshot-a-1",
		ContentSHA256: "0123456789abcdef",
	}
	if err = r.SaveSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	items, err := r.Snapshots(ctx, "snapshot-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ExecutionID != snapshot.ExecutionID || items[0].AttemptID != snapshot.AttemptID || items[0].ContentSHA256 != snapshot.ContentSHA256 {
		t.Fatalf("snapshot provenance not persisted: %+v", items)
	}
}
