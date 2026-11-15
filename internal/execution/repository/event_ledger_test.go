package repository

import (
	"context"
	"encoding/json"
		"path/filepath"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/config"
	"github.com/thkx/agentmesh/internal/platform/database"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

func TestExecutionEventLedgerCapturesFactsAndRejectsMutation(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults().Database
	cfg.DSN = filepath.Join(t.TempDir(), "execution-events.db")
	db, err := database.OpenSQLite(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = database.RunMigrations(db.DB); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(db.DB)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	input := json.RawMessage(`{"prompt":"event ledger test"}`)
	execution, err := store.Create(ctx, dom.Entity{
		ID: "ledger-exec", Kind: dom.KindAgent, Runtime: dom.RuntimeAgent,
		TaskID: "ledger-task", Status: dom.LifecycleQueued, Input: input,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Transition(ctx, execution.ID, []dom.LifecycleStatus{dom.LifecycleQueued}, dom.LifecycleRunning, "", nil); err != nil {
		t.Fatal(err)
	}
	attempt, err := store.CreateAttempt(ctx, dom.Attempt{
		ID: "ledger-attempt-1", ExecutionID: execution.ID, Number: 1,
		Runtime: dom.RuntimeAgent, Status: dom.AttemptRunning, Input: input, StartedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FinishAttempt(ctx, attempt.ID, dom.AttemptSucceeded, "runtime-task-1", []byte(`{"ok":true}`), ""); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Transition(ctx, execution.ID, []dom.LifecycleStatus{dom.LifecycleRunning}, dom.LifecycleSucceeded, "", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}

	rows, err := db.DB.QueryContext(ctx, "SELECT event_type,execution_id,COALESCE(attempt_id,'') FROM execution_events WHERE execution_id=? ORDER BY sequence", execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	var sawAttempt bool
	for rows.Next() {
		var eventType, executionID, attemptID string
		if err = rows.Scan(&eventType, &executionID, &attemptID); err != nil {
			t.Fatal(err)
		}
		if executionID != execution.ID {
			t.Fatalf("event attached to wrong execution: %s", executionID)
		}
		if attemptID == attempt.ID {
			sawAttempt = true
		}
		types = append(types, eventType)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(types) != 5 || types[0] != "execution.created" || types[1] != "execution.status_changed" ||
		types[2] != "attempt.created" || types[3] != "attempt.status_changed" || types[4] != "execution.status_changed" || !sawAttempt {
		t.Fatalf("unexpected event sequence: %+v (attempt=%v)", types, sawAttempt)
	}

	if _, err = db.DB.ExecContext(ctx, "UPDATE execution_events SET source='tampered' WHERE execution_id=?", execution.ID); err == nil {
		t.Fatal("expected append-only update trigger to reject mutation")
	}
	if _, err = db.DB.ExecContext(ctx, "DELETE FROM execution_events WHERE execution_id=?", execution.ID); err == nil {
		t.Fatal("expected append-only delete trigger to reject mutation")
	}
	var count int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM execution_events WHERE execution_id=?", execution.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("ledger rows changed after rejected mutation: %d", count)
	}
}
