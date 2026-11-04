package planworkflow

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newExecutionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE agent_goals(id TEXT PRIMARY KEY, status TEXT NOT NULL, completed_at INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE agent_plans(id TEXT PRIMARY KEY, goal_id TEXT NOT NULL REFERENCES agent_goals(id) ON DELETE CASCADE)`,
		`CREATE TABLE plan_executions(goal_id TEXT NOT NULL REFERENCES agent_goals(id) ON DELETE CASCADE, plan_id TEXT NOT NULL REFERENCES agent_plans(id) ON DELETE CASCADE, plan_version INTEGER NOT NULL, workflow_id TEXT NOT NULL, workflow_version INTEGER NOT NULL, run_id TEXT NOT NULL UNIQUE, input_hash TEXT NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY(goal_id,plan_version))`,
	} {
		if _, err = db.Exec(stmt); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO agent_goals(id,status) VALUES('goal-1','active')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO agent_plans(id,goal_id) VALUES('plan-1','goal-1')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteRepositoryReserveIsIdempotent(t *testing.T) {
	db := newExecutionTestDB(t)
	repo, err := NewSQLiteRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	record := ExecutionRecord{GoalID: "goal-1", PlanID: "plan-1", PlanVersion: 1, WorkflowID: "workflow-1", WorkflowVersion: 1, RunID: "run-1", InputHash: "hash-1", CreatedAt: time.Unix(100, 0).UTC()}
	first, err := repo.Reserve(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Reserve(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if first.RunID != second.RunID || second.InputHash != record.InputHash {
		t.Fatalf("reservation mismatch: first=%+v second=%+v", first, second)
	}
}

func TestSQLiteRepositoryRejectsChangedInput(t *testing.T) {
	db := newExecutionTestDB(t)
	repo, _ := NewSQLiteRepository(db)
	record := ExecutionRecord{GoalID: "goal-1", PlanID: "plan-1", PlanVersion: 1, WorkflowID: "workflow-1", WorkflowVersion: 1, RunID: "run-1", InputHash: "hash-1", CreatedAt: time.Now()}
	if _, err := repo.Reserve(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	record.InputHash = "different"
	if _, err := repo.Reserve(context.Background(), record); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestSQLiteRepositoryRequiresActiveGoalForNewReservation(t *testing.T) {
	db := newExecutionTestDB(t)
	repo, _ := NewSQLiteRepository(db)
	if _, err := db.Exec(`UPDATE agent_goals SET status='canceled' WHERE id='goal-1'`); err != nil {
		t.Fatal(err)
	}
	record := ExecutionRecord{GoalID: "goal-1", PlanID: "plan-1", PlanVersion: 1, WorkflowID: "workflow-1", WorkflowVersion: 1, RunID: "run-1", InputHash: "hash-1", CreatedAt: time.Now()}
	if _, err := repo.Reserve(context.Background(), record); !errors.Is(err, ErrGoalInactive) {
		t.Fatalf("expected inactive goal error, got %v", err)
	}
}
