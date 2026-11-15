package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	agentdom "github.com/thkx/agentmesh/internal/domain/agent"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/platform/database"
	"github.com/thkx/agentmesh/internal/workflow/checkpoint"
	_ "modernc.org/sqlite"
)

type blockingM11Agent struct {
	started chan struct{}
	release chan struct{}
}

func (a *blockingM11Agent) Run(ctx context.Context, task agentdom.Task) (agentdom.Result, error) {
	close(a.started)
	select {
	case <-a.release:
		return agentdom.Result{TaskID: task.ID, Status: agentdom.StatusSucceeded, Content: "long task done"}, nil
	case <-ctx.Done():
		return agentdom.Result{}, ctx.Err()
	}
}

func TestM11LongRunningNodeLeaseIsRenewedByWorkerHeartbeat(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "m11-long-node.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(8)
	if err = database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	store, err := checkpoint.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	agent := &blockingM11Agent{started: make(chan struct{}), release: make(chan struct{})}
	engine, err := New(store, agent, nil)
	if err != nil {
		t.Fatal(err)
	}
	definition := wf.Definition{ID: "m11-long", Name: "long node", Version: 1, Entry: "agent", Nodes: []wf.Node{{ID: "agent", Type: wf.NodeAgent, Agent: &wf.AgentConfig{ModelID: "model", WorkspaceID: "workspace", Prompt: "hold"}}, {ID: "end", Type: wf.NodeEnd}}, Edges: []wf.Edge{{From: "agent", To: "end"}}}
	if err = engine.PutDefinition(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.StartQueued(context.Background(), "m11-long", "m11-long-run", map[string]any{}, 3); err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(store, engine, WorkerConfig{ID: "m11-long-worker", PollInterval: 20 * time.Millisecond, LeaseDuration: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.runOne(context.Background()) }()
	select {
	case <-agent.started:
	case <-time.After(3 * time.Second):
		t.Fatal("long-running Agent did not start")
	}
	// Outlive the original lease. A second worker must not be able to claim it.
	time.Sleep(3500 * time.Millisecond)
	_, claimErr := store.ClaimRun(context.Background(), "m11-contender", 3*time.Second)
	if !errors.Is(claimErr, sql.ErrNoRows) {
		t.Fatalf("contender claimed a live long-running workflow: %v", claimErr)
	}
	close(agent.release)
	select {
	case err = <-workerDone:
		if err != nil {
			t.Fatalf("worker runOne: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-running worker did not finish")
	}
	run, err := engine.GetRun(context.Background(), "m11-long-run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != wf.RunSucceeded {
		raw, _ := json.Marshal(run)
		t.Fatalf("run did not succeed: %s", raw)
	}
}
