package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) (*sql.DB, *SQLiteStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE agent_jobs(id TEXT PRIMARY KEY,task_id TEXT NOT NULL UNIQUE,task_json TEXT NOT NULL,status TEXT NOT NULL,attempts INTEGER NOT NULL,max_attempts INTEGER NOT NULL,available_at INTEGER NOT NULL,lease_owner TEXT,lease_until INTEGER,execution_started INTEGER NOT NULL DEFAULT 0,last_error TEXT,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE agent_tool_invocations(idempotency_key TEXT PRIMARY KEY,task_id TEXT NOT NULL,tool_call_id TEXT NOT NULL,tool_name TEXT NOT NULL,status TEXT NOT NULL,result_json TEXT,error_text TEXT,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE agent_task_states(task_id TEXT PRIMARY KEY,state_json TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, store
}
func sampleTask() dom.Task {
	return dom.Task{ID: "task-1", ModelID: "model-1", WorkspaceID: "ws-1", Prompt: "hello"}
}
func TestEnqueueClaimAndComplete(t *testing.T) {
	db, s := testStore(t)
	defer db.Close()
	ctx := context.Background()
	j, err := s.Enqueue(ctx, sampleTask(), "job-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != Queued {
		t.Fatalf("status=%s", j.Status)
	}
	claimed, err := s.Claim(ctx, "worker-1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Attempts != 1 || claimed.Status != Running {
		t.Fatalf("unexpected claim: %+v", claimed)
	}
	if err = s.MarkStarted(ctx, claimed.ID, "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(ctx, claimed.ID, "worker-1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Succeeded {
		t.Fatalf("status=%s", got.Status)
	}
}
func TestRecoverExpiredLeaseBeforeExecutionRequeues(t *testing.T) {
	db, s := testStore(t)
	defer db.Close()
	ctx := context.Background()
	j, err := s.Enqueue(ctx, sampleTask(), "job-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(ctx, "worker-1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE agent_jobs SET lease_until=? WHERE id=?`, time.Now().Add(-time.Second).UnixNano(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued, interrupted, err := s.RecoverExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queued != 1 || interrupted != 0 {
		t.Fatalf("requeued=%d interrupted=%d", queued, interrupted)
	}
	got, err := s.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Queued {
		t.Fatalf("status=%s", got.Status)
	}
}
func TestRecoverExpiredAfterExecutionDoesNotReplay(t *testing.T) {
	db, s := testStore(t)
	defer db.Close()
	ctx := context.Background()
	_, err := s.Enqueue(ctx, sampleTask(), "job-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(ctx, "worker-1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkStarted(ctx, claimed.ID, "worker-1"); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE agent_jobs SET lease_until=? WHERE id=?`, time.Now().Add(-time.Second).UnixNano(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued, interrupted, err := s.RecoverExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queued != 0 || interrupted != 1 {
		t.Fatalf("requeued=%d interrupted=%d", queued, interrupted)
	}
	got, err := s.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Interrupted {
		t.Fatalf("status=%s", got.Status)
	}
}
func TestFailureRetriesOnlyBeforeToolSideEffects(t *testing.T) {
	db, s := testStore(t)
	defer db.Close()
	ctx := context.Background()
	_, err := s.Enqueue(ctx, sampleTask(), "job-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim(ctx, "worker-1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkStarted(ctx, claimed.ID, "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err = s.Fail(ctx, claimed.ID, "worker-1", errors.New("temporary model failure")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Queued {
		t.Fatalf("expected safe retry queued, got %s", got.Status)
	}
	if _, err = db.Exec(`UPDATE agent_jobs SET available_at=? WHERE id=?`, time.Now().Add(-time.Second).UnixNano(), claimed.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.Claim(ctx, "worker-1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkStarted(ctx, claimed.ID, "worker-1"); err != nil {
		t.Fatal(err)
	}
	ledger, _ := NewToolLedger(db)
	started, err := ledger.Begin(ctx, "key-1", "task-1", "call-1", "file.write")
	if err != nil || !started {
		t.Fatalf("begin started=%v err=%v", started, err)
	}
	if err = s.Fail(ctx, claimed.ID, "worker-1", errors.New("after tool")); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Failed {
		t.Fatalf("expected terminal failure, got %s", got.Status)
	}
}
func TestToolLedgerRefusesIndeterminateReplayAndReturnsCompleted(t *testing.T) {
	db, _ := testStore(t)
	defer db.Close()
	l, _ := NewToolLedger(db)
	ctx := context.Background()
	started, err := l.Begin(ctx, "key-1", "task-1", "call-1", "file.write")
	if err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	if _, _, _, err = l.Load(ctx, "key-1"); !errors.Is(err, ErrIndeterminate) {
		t.Fatalf("expected indeterminate, got %v", err)
	}
	if err = l.Complete(ctx, "key-1", map[string]string{"ok": "yes"}, nil); err != nil {
		t.Fatal(err)
	}
	started, err = l.Begin(ctx, "key-1", "task-1", "call-1", "file.write")
	if err != nil || started {
		t.Fatalf("duplicate begin started=%v err=%v", started, err)
	}
	raw, storedErr, completed, err := l.Load(ctx, "key-1")
	if err != nil || !completed || storedErr != "" || len(raw) == 0 {
		t.Fatalf("load raw=%s error=%s completed=%v err=%v", raw, storedErr, completed, err)
	}
}

func TestEnqueueIsIdempotentForSameTaskPayload(t *testing.T) {
	db, s := testStore(t)
	defer db.Close()
	ctx := context.Background()
	first, err := s.Enqueue(ctx, sampleTask(), "job-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Enqueue(ctx, sampleTask(), "job-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.Status != Queued {
		t.Fatalf("not idempotent: first=%+v second=%+v", first, second)
	}
	changed := sampleTask()
	changed.Prompt = "different"
	if _, err := s.Enqueue(ctx, changed, "job-1", 3); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestCancelQueuedTask(t *testing.T) {
	db, s := testStore(t)
	defer db.Close()
	ctx := context.Background()
	_, err := s.Enqueue(ctx, sampleTask(), "job-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelTask(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
	job, err := s.Get(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != Canceled {
		t.Fatalf("status=%s", job.Status)
	}
	if _, err := s.Claim(ctx, "worker-1", 5*time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled job was claimable: %v", err)
	}
}

func TestCanceledExecutionIsNotRetried(t *testing.T) {
	db, s := testStore(t)
	defer db.Close()
	ctx := context.Background()
	_, err := s.Enqueue(ctx, sampleTask(), "job-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(ctx, "worker-1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkStarted(ctx, job.ID, "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err = s.Fail(ctx, job.ID, "worker-1", context.Canceled); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Canceled {
		t.Fatalf("expected canceled, got %s", got.Status)
	}
}

func TestRecoverExpiredFailedModelOnlyTaskRetries(t *testing.T) {
	db, s := testStore(t)
	defer db.Close()
	ctx := context.Background()
	_, err := s.Enqueue(ctx, sampleTask(), "job-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(ctx, "worker-1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkStarted(ctx, job.ID, "worker-1"); err != nil {
		t.Fatal(err)
	}
	state := `{"result":{"status":"failed","tool_calls":0}}`
	now := time.Now().UTC().UnixNano()
	if _, err = db.Exec(`INSERT INTO agent_task_states(task_id,state_json,created_at,updated_at) VALUES(?,?,?,?)`, "task-1", state, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE agent_jobs SET lease_until=? WHERE id=?`, time.Now().Add(-time.Second).UnixNano(), job.ID); err != nil {
		t.Fatal(err)
	}
	requeued, interrupted, err := s.RecoverExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if requeued != 1 || interrupted != 0 {
		t.Fatalf("requeued=%d interrupted=%d", requeued, interrupted)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Queued {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestManualRequeueRequiresNoToolIntent(t *testing.T) {
	db, s := testStore(t)
	defer db.Close()
	ctx := context.Background()
	_, err := s.Enqueue(ctx, sampleTask(), "job-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(ctx, "worker-1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkStarted(ctx, job.ID, "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err = s.Fail(ctx, job.ID, "worker-1", errors.New("model unavailable")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Requeue(ctx, job.ID); err != nil {
		t.Fatalf("safe model-only requeue failed: %v", err)
	}
}

func TestManualRequeueRefusesToolSideEffect(t *testing.T) {
	db, s := testStore(t)
	defer db.Close()
	ctx := context.Background()
	_, err := s.Enqueue(ctx, sampleTask(), "job-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(ctx, "worker-1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkStarted(ctx, job.ID, "worker-1"); err != nil {
		t.Fatal(err)
	}
	ledger, _ := NewToolLedger(db)
	started, err := ledger.Begin(ctx, "key-1", "task-1", "call-1", "file.write")
	if err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	if err = s.Fail(ctx, job.ID, "worker-1", errors.New("tool outcome unknown")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Requeue(ctx, job.ID); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("expected unsafe requeue rejection, got %v", err)
	}
}
