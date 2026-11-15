package checkpoint_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/platform/database"
	"github.com/thkx/agentmesh/internal/workflow/checkpoint"
)

func newM9Store(t *testing.T) (*sql.DB, *checkpoint.Store) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "m9-checkpoint-test.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	if err = database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	store, err := checkpoint.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, store
}

func createM9Run(t *testing.T, store *checkpoint.Store, id string) {
	t.Helper()
	ctx := context.Background()
	d := workflow.Definition{ID: "m9", Name: "M9", Version: 1, Entry: "agent", Nodes: []workflow.Node{{ID: "agent", Type: workflow.NodeAgent, Agent: &workflow.AgentConfig{ModelID: "m", WorkspaceID: "w", Prompt: "p"}}, {ID: "end", Type: workflow.NodeEnd}}, Edges: []workflow.Edge{{From: "agent", To: "end"}}}
	if err := store.SaveDefinition(ctx, d); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r := workflow.Run{ID: id, WorkflowID: "m9", WorkflowVersion: 1, Status: workflow.RunQueued, Input: json.RawMessage(`{}`), Context: map[string]any{}, Nodes: map[string]workflow.NodeState{}, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueRun(ctx, id, 4); err != nil {
		t.Fatal(err)
	}
}

func TestM9ConcurrentWorkersOnlyOneClaimsJob(t *testing.T) {
	db, store := newM9Store(t)
	_ = db
	createM9Run(t, store, "race-run")
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, owner := range []string{"worker-a", "worker-b"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			_, err := store.ClaimRun(ctx, owner, 5*time.Second)
			results <- err
		}(owner)
	}
	wg.Wait()
	close(results)
	claimed := 0
	for err := range results {
		if err == nil {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("expected exactly one worker to claim job, got %d", claimed)
	}
}

func TestM9ExpiredLeaseIsRecoveredAndRecorded(t *testing.T) {
	db, store := newM9Store(t)
	createM9Run(t, store, "crash-run")
	ctx := context.Background()
	job, err := store.ClaimRun(ctx, "worker-crashed", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE workflow_jobs SET lease_until=0 WHERE run_id=?`, job.RunID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := store.ClaimRun(ctx, "worker-recovery", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.RunID != "crash-run" || reclaimed.Owner != "worker-recovery" {
		t.Fatalf("unexpected reclaimed job: %+v", reclaimed)
	}
	events, err := store.Events(ctx, "crash-run", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	foundExpired, foundClaim := false, false
	for _, event := range events {
		if event.Type == "worker.lease_expired" {
			foundExpired = true
		}
		if event.Type == "worker.claimed" && event.WorkerID == "worker-recovery" {
			foundClaim = true
		}
	}
	if !foundExpired || !foundClaim {
		t.Fatalf("missing crash recovery events: expired=%v reclaimed=%v", foundExpired, foundClaim)
	}
}

func TestM9OperatorCanRetryFailedNodeAtomically(t *testing.T) {
	db, store := newM9Store(t)
	createM9Run(t, store, "manual-run")
	ctx := context.Background()
	run, err := store.GetRun(ctx, "manual-run")
	if err != nil {
		t.Fatal(err)
	}
	run.Status = workflow.RunFailed
	run.Error = "temporary provider error"
	run.Nodes["agent"] = workflow.NodeState{Status: workflow.NodeFailed, Error: run.Error, Attempt: 1}
	b, _ := json.Marshal(run)
	if _, err = db.Exec(`UPDATE workflow_runs SET status='failed',state_json=? WHERE id=?`, string(b), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE workflow_jobs SET status='failed',last_error=? WHERE run_id=?`, run.Error, run.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.RecoverFailedNode(ctx, run.ID, "agent", "retry", "operator approved retry", 4)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != workflow.RunQueued || recovered.Nodes["agent"].Status != workflow.NodePending {
		t.Fatalf("unexpected recovery state: %+v", recovered)
	}
	var jobStatus string
	if err = db.QueryRow(`SELECT status FROM workflow_jobs WHERE run_id=?`, run.ID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "queued" {
		t.Fatalf("job status=%s", jobStatus)
	}
	events, err := store.Events(ctx, run.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "run.operator_recovery" {
			found = true
		}
	}
	if !found {
		t.Fatal("operator recovery event not persisted")
	}
}

func TestM9InterruptedNodeCannotBeAutomaticallyRetried(t *testing.T) {
	db, store := newM9Store(t)
	createM9Run(t, store, "interrupted-run")
	ctx := context.Background()
	run, err := store.GetRun(ctx, "interrupted-run")
	if err != nil {
		t.Fatal(err)
	}
	run.Status = workflow.RunFailed
	run.Nodes["agent"] = workflow.NodeState{Status: workflow.NodeInterrupted, Error: "external side effect outcome unknown"}
	b, _ := json.Marshal(run)
	if _, err = db.Exec(`UPDATE workflow_runs SET status='failed',state_json=? WHERE id=?`, string(b), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE workflow_jobs SET status='interrupted' WHERE run_id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RecoverFailedNode(ctx, run.ID, "agent", "retry", "", 3); err == nil {
		t.Fatal("expected retry of an interrupted node to be rejected")
	}
}

func TestWorkflowFencingRejectsStaleWorkerWrites(t *testing.T) {
	db, store := newM9Store(t)
	createM9Run(t, store, "fenced-run")
	ctx := context.Background()
	oldJob, err := store.ClaimRun(ctx, "same-worker-id", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if oldJob.FenceToken < 1 {
		t.Fatalf("expected positive fencing token: %+v", oldJob)
	}
	if _, err = db.Exec(`UPDATE workflow_jobs SET lease_until=0 WHERE run_id=?`, oldJob.RunID); err != nil {
		t.Fatal(err)
	}
	newJob, err := store.ClaimRun(ctx, "same-worker-id", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if newJob.FenceToken <= oldJob.FenceToken {
		t.Fatalf("fencing token did not advance: old=%d new=%d", oldJob.FenceToken, newJob.FenceToken)
	}
	if err = store.HeartbeatRun(ctx, oldJob, 10*time.Second); err == nil {
		t.Fatal("stale worker heartbeat was accepted")
	}

	run, err := store.GetRun(ctx, "fenced-run")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveRunFenced(ctx, run, oldJob.Owner, oldJob.FenceToken); err == nil {
		t.Fatal("stale worker checkpoint write was accepted")
	}
	if err = store.SaveRunFenced(ctx, run, newJob.Owner, newJob.FenceToken); err != nil {
		t.Fatalf("current worker checkpoint write rejected: %v", err)
	}

	if _, _, err = store.BeginNode(ctx, "fenced-run", "agent", "fenced-run:agent", oldJob.Owner, oldJob.FenceToken, time.Minute); err == nil {
		t.Fatal("stale worker began a node")
	}
	if _, _, err = store.BeginNode(ctx, "fenced-run", "agent", "fenced-run:agent", newJob.Owner, newJob.FenceToken, time.Minute); err != nil {
		t.Fatalf("current worker could not begin node: %v", err)
	}
	if err = store.CompleteNode(ctx, "fenced-run", "agent", oldJob.Owner, oldJob.FenceToken, json.RawMessage(`{"ok":true}`), nil); err == nil {
		t.Fatal("stale worker completed a node")
	}
	if err = store.CompleteNode(ctx, "fenced-run", "agent", newJob.Owner, newJob.FenceToken, json.RawMessage(`{"ok":true}`), nil); err != nil {
		t.Fatalf("current worker could not complete node: %v", err)
	}
}

func TestWorkflowHeartbeatRenewsLongRunningNodeLease(t *testing.T) {
	db, store := newM9Store(t)
	createM9Run(t, store, "renew-run")
	ctx := context.Background()
	job, err := store.ClaimRun(ctx, "worker-renew", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.BeginNode(ctx, job.RunID, "agent", "renew-run:agent", job.Owner, job.FenceToken, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err = store.HeartbeatRun(ctx, job, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	var nodeLease, runLease int64
	if err = db.QueryRow(`SELECT lease_until FROM workflow_node_executions WHERE run_id=? AND node_id='agent'`, job.RunID).Scan(&nodeLease); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT lease_until FROM workflow_jobs WHERE run_id=?`, job.RunID).Scan(&runLease); err != nil {
		t.Fatal(err)
	}
	if nodeLease != runLease {
		t.Fatalf("node lease was not renewed with run lease: node=%d run=%d", nodeLease, runLease)
	}
}
