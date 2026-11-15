package repository

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/config"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
	"github.com/thkx/agentmesh/internal/platform/database"
)

func TestLifecycleEnforcementAndDurableCancellationIntent(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults().Database
	cfg.DSN = filepath.Join(t.TempDir(), "lifecycle-enforcement.db")
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
	input := json.RawMessage(`{"prompt":"lifecycle test"}`)
	execution, err := store.Create(ctx, dom.Entity{
		ID: "lifecycle-exec", Kind: dom.KindAgent, Runtime: dom.RuntimeAgent,
		TaskID: "lifecycle-task", Status: dom.LifecycleQueued, Input: input,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Intent is durable and idempotent, but it does not claim the runtime stopped.
	if err = store.RequestCancel(ctx, execution.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.RequestCancel(ctx, execution.ID); err != nil {
		t.Fatal(err)
	}
	var requests int
	var eventType string
	if err = db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(event_type)
		FROM execution_control_events WHERE execution_id=?
	`, execution.ID).Scan(&requests, &eventType); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || eventType != "execution.cancel_requested" {
		t.Fatalf("cancel events=%d type=%q; want one durable cancellation request", requests, eventType)
	}
	current, err := store.Get(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != dom.LifecycleQueued {
		t.Fatalf("cancel request changed execution outcome to %q", current.Status)
	}

	if _, err = store.Transition(ctx, execution.ID, []dom.LifecycleStatus{dom.LifecycleQueued},
		dom.LifecycleRunning, "", nil); err != nil {
		t.Fatal(err)
	}
	// A started Execution cannot regress to Queued, even through raw SQL.
	if _, err = db.DB.ExecContext(ctx,
		"UPDATE executions SET status='queued' WHERE id=?", execution.ID); err == nil {
		t.Fatal("database allowed a running execution to regress to queued")
	}
	attempt, err := store.CreateAttempt(ctx, dom.Attempt{
		ID: "lifecycle-attempt-1", ExecutionID: execution.ID, Number: 1,
		Runtime: dom.RuntimeAgent, Status: dom.AttemptRunning, Input: input, StartedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FinishAttempt(ctx, attempt.ID, dom.AttemptSucceeded, "", []byte(`{"ok":true}`), ""); err != nil {
		t.Fatal(err)
	}

	// A terminal Attempt cannot be reopened, even by bypassing the domain API.
	if _, err = db.DB.ExecContext(ctx,
		"UPDATE execution_attempts SET status='running' WHERE id=?", attempt.ID); err == nil {
		t.Fatal("database allowed a terminal attempt to be reopened")
	}
	if _, err = store.Transition(ctx, execution.ID, []dom.LifecycleStatus{dom.LifecycleRunning},
		dom.LifecycleSucceeded, "", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	// A terminal Execution is immutable at the persistence boundary too.
	if _, err = db.DB.ExecContext(ctx,
		"UPDATE executions SET status='running' WHERE id=?", execution.ID); err == nil {
		t.Fatal("database allowed a terminal execution to be reopened")
	}
	// Execution identity/input cannot be rewritten after acceptance.
	if _, err = db.DB.ExecContext(ctx,
		"UPDATE executions SET input_json='{}' WHERE id=?", execution.ID); err == nil {
		t.Fatal("database allowed accepted execution input to be rewritten")
	}
}
