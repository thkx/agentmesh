package runtime

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/platform/database"
	"github.com/thkx/agentmesh/internal/workflow/checkpoint"
)

func TestP5TwoNodeWorkflowRunsOnSQLiteAndSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "p5-workflow.db")
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	store, err := checkpoint.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	agent := &fakeAgent{}
	engine, err := New(store, agent, nil)
	if err != nil {
		t.Fatal(err)
	}

	definition := workflow.Definition{
		ID: "p5-durable-two-node", Name: "P5 durable two-node workflow", Version: 1, Entry: "draft",
		Nodes: []workflow.Node{
			{ID: "draft", Type: workflow.NodeAgent, Agent: &workflow.AgentConfig{ModelID: "test-model", WorkspaceID: "test-workspace", Prompt: "draft"}},
			{ID: "review", Type: workflow.NodeAgent, Agent: &workflow.AgentConfig{ModelID: "test-model", WorkspaceID: "test-workspace", Prompt: "review"}},
			{ID: "finish", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{{From: "draft", To: "review"}, {From: "review", To: "finish"}},
	}
	if err := engine.PutDefinition(ctx, definition); err != nil {
		t.Fatalf("save workflow definition: %v", err)
	}
	run, err := engine.StartQueued(ctx, definition.ID, "p5-durable-run", map[string]any{"topic": "acceptance"}, 3)
	if err != nil {
		t.Fatalf("queue workflow run: %v", err)
	}
	if run.Status != workflow.RunQueued {
		t.Fatalf("initial run status=%s want=%s", run.Status, workflow.RunQueued)
	}

	worker, err := NewWorker(store, engine, WorkerConfig{ID: "p5-test-worker", LeaseDuration: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.runOne(ctx); err != nil {
		t.Fatalf("process queued workflow: %v", err)
	}
	run, err = engine.GetRun(ctx, "p5-durable-run")
	if err != nil {
		t.Fatalf("get completed run: %v", err)
	}
	if run.Status != workflow.RunSucceeded {
		t.Fatalf("run status=%s error=%s", run.Status, run.Error)
	}
	if run.Nodes["draft"].Status != workflow.NodeSucceeded || run.Nodes["review"].Status != workflow.NodeSucceeded {
		t.Fatalf("both agent nodes must succeed: %#v", run.Nodes)
	}
	if agent.calls != 2 {
		t.Fatalf("agent calls=%d want=2", agent.calls)
	}

	events, err := engine.Events(ctx, run.ID, 0, 100)
	if err != nil {
		t.Fatalf("read workflow events: %v", err)
	}
	if len(events) < 6 {
		t.Fatalf("workflow event history too short: %d", len(events))
	}
	observations, err := engine.NodeObservations(ctx, run.ID)
	if err != nil {
		t.Fatalf("read node observations: %v", err)
	}
	if len(observations) < 2 {
		t.Fatalf("node observations=%d want at least 2", len(observations))
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close SQLite database: %v", err)
	}

	reopened, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopenedStore, err := checkpoint.NewSQLite(reopened)
	if err != nil {
		t.Fatal(err)
	}
	reopenedEngine, err := New(reopenedStore, &fakeAgent{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reopenedEngine.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("read run after database reopen: %v", err)
	}
	if persisted.Status != workflow.RunSucceeded || persisted.Nodes["review"].Status != workflow.NodeSucceeded {
		t.Fatalf("workflow state did not survive database reopen: %#v", persisted)
	}
}
