package state

import (
	"context"
	"database/sql"
	"testing"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
	"github.com/thkx/agentmesh/internal/modelgateway"
	_ "modernc.org/sqlite"
)

func TestSQLiteStoreRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE agent_task_states(task_id TEXT PRIMARY KEY, state_json TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{Task: dom.Task{ID: "task-1", ModelID: "model-1", WorkspaceID: "ws-1", Prompt: "hello"}, Result: dom.Result{TaskID: "task-1", Status: dom.StatusSucceeded, Content: "done"}, Messages: []modelgateway.Message{{Role: modelgateway.RoleUser, Content: "hello"}}}
	if err := store.Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Content != "done" || len(got.Messages) != 1 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
}
