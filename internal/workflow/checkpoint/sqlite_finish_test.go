package checkpoint_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/domain/workflow"
)

func TestFinishRunPersistsFailureToRunAndJob(t *testing.T) {
	db, store := newM9Store(t)
	createM9Run(t, store, "finish-failure-run")
	ctx := context.Background()

	job, err := store.ClaimRun(ctx, "worker-failure", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.GetRun(ctx, job.RunID)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("database is locked (5) (SQLITE_BUSY)")
	if err = store.FinishRun(ctx, job, run, cause); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	persisted, err := store.GetRun(ctx, job.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != workflow.RunFailed {
		t.Fatalf("run status = %q, want failed", persisted.Status)
	}
	if persisted.Error != cause.Error() {
		t.Fatalf("run error = %q, want %q", persisted.Error, cause.Error())
	}

	var jobStatus, lastError string
	if err = db.QueryRowContext(ctx, `SELECT status,last_error FROM workflow_jobs WHERE run_id=?`, job.RunID).Scan(&jobStatus, &lastError); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "failed" || lastError != cause.Error() {
		t.Fatalf("job status/error = %q/%q, want failed/%q", jobStatus, lastError, cause.Error())
	}

	events, err := store.Events(ctx, job.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var finished bool
	for _, event := range events {
		if event.Type != "worker.finished" {
			continue
		}
		finished = true
		var data map[string]any
		if err = json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data["job_status"] != "failed" || data["run_status"] != string(workflow.RunFailed) {
			t.Fatalf("worker.finished data = %#v", data)
		}
	}
	if !finished {
		t.Fatal("worker.finished event was not persisted")
	}
}

func TestFinishRunRejectsStaleFencingToken(t *testing.T) {
	db, store := newM9Store(t)
	createM9Run(t, store, "finish-stale-run")
	ctx := context.Background()

	oldJob, err := store.ClaimRun(ctx, "worker-old", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE workflow_jobs SET lease_until=0 WHERE run_id=?`, oldJob.RunID); err != nil {
		t.Fatal(err)
	}
	newJob, err := store.ClaimRun(ctx, "worker-new", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if newJob.FenceToken <= oldJob.FenceToken {
		t.Fatalf("fencing token did not advance: old=%d new=%d", oldJob.FenceToken, newJob.FenceToken)
	}

	run, err := store.GetRun(ctx, oldJob.RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = workflow.RunFailed
	run.Error = "stale worker failure"
	if err = store.FinishRun(ctx, oldJob, run, errors.New(run.Error)); err == nil {
		t.Fatal("stale worker was allowed to finish a newer lease")
	}

	persisted, err := store.GetRun(ctx, oldJob.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != workflow.RunQueued {
		t.Fatalf("stale worker changed run status to %q", persisted.Status)
	}
	var status string
	if err = db.QueryRowContext(ctx, `SELECT status FROM workflow_jobs WHERE run_id=?`, oldJob.RunID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("new worker job status = %q, want running", status)
	}
}

func TestSaveRunFencedSerializesWithConcurrentHeartbeats(t *testing.T) {
	_, store := newM9Store(t)
	createM9Run(t, store, "checkpoint-heartbeat-race")
	ctx := context.Background()

	job, err := store.ClaimRun(ctx, "worker-race", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 80; i++ {
			if err := store.HeartbeatRun(ctx, job, 30*time.Second); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 80; i++ {
			run, err := store.GetRun(ctx, job.RunID)
			if err != nil {
				errs <- err
				return
			}
			run.UpdatedAt = time.Now().UTC()
			if err = store.SaveRunFenced(ctx, run, job.Owner, job.FenceToken); err != nil {
				errs <- err
				return
			}
		}
	}()

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent heartbeat/checkpoint operation failed: %v", err)
	}
}
