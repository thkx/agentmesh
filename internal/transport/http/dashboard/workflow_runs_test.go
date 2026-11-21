package dashboard

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	agentdom "github.com/thkx/agentmesh/internal/domain/agent"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/workflow/checkpoint"
	workflowruntime "github.com/thkx/agentmesh/internal/workflow/runtime"
	_ "modernc.org/sqlite"
)

func TestDashboardListsAndShowsPersistedWorkflowRuns(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	statements := []string{
		`CREATE TABLE workflows (id TEXT NOT NULL, version INTEGER NOT NULL, name TEXT NOT NULL, definition_json TEXT NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY(id,version))`,
		`CREATE TABLE workflow_runs (id TEXT PRIMARY KEY NOT NULL, workflow_id TEXT NOT NULL, workflow_version INTEGER NOT NULL, status TEXT NOT NULL, current_node TEXT, state_json TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE TABLE workflow_events (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, sequence INTEGER NOT NULL, event_type TEXT NOT NULL, node_id TEXT, worker_id TEXT, occurred_at INTEGER NOT NULL, data_json TEXT NOT NULL DEFAULT '{}')`,
		`CREATE TABLE workflow_node_executions (run_id TEXT NOT NULL,node_id TEXT NOT NULL,idempotency_key TEXT NOT NULL,status TEXT NOT NULL,owner TEXT,lease_until INTEGER,output_json TEXT,error_text TEXT,attempt INTEGER NOT NULL DEFAULT 1,started_at INTEGER NOT NULL,finished_at INTEGER,PRIMARY KEY(run_id,node_id))`,
		`CREATE TABLE workflow_jobs (id TEXT PRIMARY KEY,run_id TEXT NOT NULL UNIQUE,status TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,max_attempts INTEGER NOT NULL DEFAULT 3,available_at INTEGER NOT NULL,lease_owner TEXT,lease_until INTEGER,last_error TEXT,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	definition := wf.Definition{
		ID: "wf-live", Name: "Live Acceptance Workflow", Version: 1, Entry: "start",
		Nodes: []wf.Node{
			{ID: "start", Type: wf.NodeStart},
			{ID: "agent-a", Type: wf.NodeAgent},
			{ID: "agent-b", Type: wf.NodeAgent},
		},
		Edges: []wf.Edge{
			{From: "start", To: "agent-a"},
			{From: "agent-a", To: "agent-b"},
		},
	}

	// Persist the definition and run using the production SQLite checkpoint store.
	store, err := checkpoint.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = store.SaveDefinition(context.Background(), definition); err != nil {
		t.Fatal(err)
	}

	run := wf.Run{
		ID: "p5-live-run-test", WorkflowID: "wf-live", WorkflowVersion: 1,
		Status: wf.RunQueued, CreatedAt: now, UpdatedAt: now,
		Nodes: map[string]wf.NodeState{
			"start":   {Status: wf.NodeSucceeded, Attempt: 1},
			"agent-a": {Status: wf.NodeSucceeded, Attempt: 1, DurationMillis: 25},
			"agent-b": {Status: wf.NodeSucceeded, Attempt: 1, DurationMillis: 30},
		},
		Context: map[string]any{},
	}
	if err = store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	run.Status = wf.RunSucceeded
	run.FinishedAt = now
	run.UpdatedAt = now
	if err = store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	// The Dashboard uses the real Engine read APIs; no model call is started.
	engine, err := workflowruntime.New(store, dashboardNoopAgent{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	New(Dependencies{DB: db, WorkflowEngine: engine}).RegisterRoutes(router)

	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/dashboard/workflows", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	for _, want := range []string{"p5-live-run-test", "Live Acceptance Workflow", "succeeded", "View run"} {
		if !strings.Contains(list.Body.String(), want) {
			t.Fatalf("workflow list missing %q: %s", want, list.Body.String())
		}
	}

	detail := httptest.NewRecorder()
	router.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/dashboard/workflow-runs/p5-live-run-test", nil))
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	for _, want := range []string{"Workflow Run Details", "agent-a", "agent-b", "Node observations", "Workflow event history", "run.succeeded"} {
		if !strings.Contains(detail.Body.String(), want) {
			t.Fatalf("workflow detail missing %q: %s", want, detail.Body.String())
		}
	}
}

type dashboardNoopAgent struct{}

func (dashboardNoopAgent) Run(context.Context, agentdom.Task) (agentdom.Result, error) {
	return agentdom.Result{}, nil
}

func TestQueueingWorkflowRedirectsToDurableRunDetail(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`CREATE TABLE workflows (id TEXT NOT NULL, version INTEGER NOT NULL, name TEXT NOT NULL, definition_json TEXT NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY(id,version))`,
		`CREATE TABLE workflow_runs (id TEXT PRIMARY KEY NOT NULL, workflow_id TEXT NOT NULL, workflow_version INTEGER NOT NULL, status TEXT NOT NULL, current_node TEXT, state_json TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE TABLE workflow_events (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, sequence INTEGER NOT NULL, event_type TEXT NOT NULL, node_id TEXT, worker_id TEXT, occurred_at INTEGER NOT NULL, data_json TEXT NOT NULL DEFAULT '{}')`,
		`CREATE TABLE workflow_node_executions (run_id TEXT NOT NULL,node_id TEXT NOT NULL,idempotency_key TEXT NOT NULL,status TEXT NOT NULL,owner TEXT,lease_until INTEGER,output_json TEXT,error_text TEXT,attempt INTEGER NOT NULL DEFAULT 1,started_at INTEGER NOT NULL,finished_at INTEGER,PRIMARY KEY(run_id,node_id))`,
		`CREATE TABLE workflow_jobs (id TEXT PRIMARY KEY,run_id TEXT NOT NULL UNIQUE,status TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,max_attempts INTEGER NOT NULL DEFAULT 3,available_at INTEGER NOT NULL,lease_owner TEXT,lease_until INTEGER,last_error TEXT,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	store, err := checkpoint.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	definition := wf.Definition{
		ID: "wf-quickstart-redirect", Name: "Quickstart redirect", Version: 1, Entry: "start",
		Nodes: []wf.Node{{ID: "start", Type: wf.NodeStart}, {ID: "end", Type: wf.NodeEnd}},
		Edges: []wf.Edge{{From: "start", To: "end"}},
	}
	if err := store.SaveDefinition(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	engine, err := workflowruntime.New(store, dashboardNoopAgent{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	New(Dependencies{DB: db, WorkflowEngine: engine}).RegisterRoutes(router)
	req := httptest.NewRequest(http.MethodPost, "/dashboard/workflows/wf-quickstart-redirect/run", strings.NewReader("input_json=%7B%7D"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1")
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("queue status=%d body=%s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "/dashboard/workflow-runs/") {
		t.Fatalf("queue should redirect to durable run detail, location=%q", location)
	}
	runID := strings.TrimPrefix(location, "/dashboard/workflow-runs/")
	if runID == "" {
		t.Fatal("redirect did not include durable run ID")
	}
	detail := httptest.NewRecorder()
	router.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, location, nil))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), runID) {
		t.Fatalf("durable run detail status=%d body=%s", detail.Code, detail.Body.String())
	}
}
