package collaboration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentcatalog "github.com/thkx/agentmesh/internal/agentcatalog"
	"github.com/thkx/agentmesh/internal/collaboration"
	agentdom "github.com/thkx/agentmesh/internal/domain/agent"
	execdom "github.com/thkx/agentmesh/internal/domain/execution"
	executionmanager "github.com/thkx/agentmesh/internal/execution/manager"
	executionrepository "github.com/thkx/agentmesh/internal/execution/repository"
	"github.com/thkx/agentmesh/internal/workspace"
)

type immediateSuccessAdapter struct {
	submitted chan execdom.Entity
}

func (a immediateSuccessAdapter) Submit(_ context.Context, entity execdom.Entity) (string, error) {
	if a.submitted != nil {
		a.submitted <- entity
	}
	return "immediate-success", nil
}
func (immediateSuccessAdapter) Observe(context.Context, execdom.Entity) (executionmanager.Observation, error) {
	result, _ := json.Marshal(map[string]string{"content": "completed by test runtime"})
	return executionmanager.Observation{Status: execdom.LifecycleSucceeded, Attempt: 1, Result: result}, nil
}
func (immediateSuccessAdapter) Cancel(context.Context, execdom.Entity) error { return nil }

type staticMemoryRetriever struct {
	value string
}

func (r staticMemoryRetriever) Retrieve(context.Context, string, string, []string, int) (string, error) {
	return r.value, nil
}

type unusedCatalogRunner struct{}

func (unusedCatalogRunner) Run(context.Context, agentdom.Task) (agentdom.Result, error) {
	return agentdom.Result{}, errors.New("catalog runner must not be called by execution manager path")
}

func TestWakeupExecutesAssignedTaskThroughExecutionManager(t *testing.T) {
	ctx := context.Background()
	service, db := newInboxTestService(t)
	defer db.Close()

	now := time.Now().UTC().UnixNano()
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO providers(id,type,name,kind,enabled,created_at,updated_at)
		VALUES('provider-test','openai','Test Provider','cloud',1,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO models(id,provider_id,name,display_name,context_window,max_output_tokens,supports_streaming,enabled,created_at,updated_at)
		VALUES('model-test','provider-test','test-model','Test Model',8192,1024,1,1,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}

	catalogRepo, err := agentcatalog.NewSQLiteRepository(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	definition := agentdom.Definition{
		ID: "definition-test", Name: "Integration Agent", SystemPrompt: "You are an integration test agent.",
		ModelID: "model-test", Temperature: 0, MaxSteps: 4, MaxToolCalls: 4, MaxOutputTokens: 1024,
		TimeoutSeconds: 30, Enabled: true,
	}
	if err = catalogRepo.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	definition, err = catalogRepo.Get(ctx, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := agentcatalog.NewService(catalogRepo, unusedCatalogRunner{})
	if err != nil {
		t.Fatal(err)
	}

	collabWorkspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Wakeup Integration", Slug: "wakeup-integration"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: collabWorkspace.ID, Name: "Executor", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	filesystemWorkspaces, err := workspace.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = filesystemWorkspaces.Create(ctx, "agent-work"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.BindRuntime(ctx, identity.ID, "agent-work", definition); err != nil {
		t.Fatal(err)
	}

	executionStore, err := executionrepository.NewSQLiteStore(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	executions, err := executionmanager.New(executionStore)
	if err != nil {
		t.Fatal(err)
	}
	submitted := make(chan execdom.Entity, 1)
	if err = executions.Register(execdom.RuntimeAgent, immediateSuccessAdapter{submitted: submitted}); err != nil {
		t.Fatal(err)
	}

	task, err := service.CreateTask(ctx, collaboration.Task{WorkspaceID: collabWorkspace.ID, Title: "Run integration task", Description: "Verify durable collaboration wakeup."})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := service.AssignTask(ctx, task.ID, identity.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := collaboration.NewDispatcher(db.DB, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = dispatcher.DispatchOnce(ctx, 100); err != nil {
		t.Fatal(err)
	}

	wakeup, err := collaboration.NewWakeupWorker(service, definitions, executions, filesystemWorkspaces, collaboration.WakeupConfig{
		WorkerID: "integration", PollInterval: 5 * time.Millisecond, InboxLease: time.Minute,
		AssignmentLease: time.Minute, MaxInboxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	wakeup.SetMemoryRetriever(staticMemoryRetriever{value: "Remember: this workspace requires migration-safe schema changes."})
	workerCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- wakeup.Run(workerCtx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var assignmentStatus, inboxStatus string
		err = db.DB.QueryRowContext(ctx, "SELECT status FROM task_assignments WHERE id=?", assignment.ID).Scan(&assignmentStatus)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		err = db.DB.QueryRowContext(ctx, "SELECT status FROM collaboration_inbox WHERE agent_id=? AND event_type='task.assigned' ORDER BY received_at DESC LIMIT 1", identity.ID).Scan(&inboxStatus)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if assignmentStatus == "completed" && inboxStatus == "handled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var assignmentStatus, taskStatus string
	if err = db.DB.QueryRowContext(ctx, "SELECT status FROM task_assignments WHERE id=?", assignment.ID).Scan(&assignmentStatus); err != nil {
		cancel()
		t.Fatal(err)
	}
	if err = db.DB.QueryRowContext(ctx, "SELECT status FROM collaboration_tasks WHERE id=?", task.ID).Scan(&taskStatus); err != nil {
		cancel()
		t.Fatal(err)
	}
	if assignmentStatus != "completed" || taskStatus != "completed" {
		cancel()
		t.Fatalf("wakeup did not complete task: assignment=%s task=%s", assignmentStatus, taskStatus)
	}
	select {
	case submittedExecution := <-submitted:
		var submittedTask agentdom.Task
		if err = json.Unmarshal(submittedExecution.Input, &submittedTask); err != nil {
			cancel()
			t.Fatal(err)
		}
		if !strings.Contains(submittedTask.SystemPrompt, "migration-safe schema changes") {
			cancel()
			t.Fatalf("long-term memory was not injected into runtime context: %q", submittedTask.SystemPrompt)
		}
	default:
		cancel()
		t.Fatal("execution adapter did not receive the assigned task")
	}
	session, err := service.GetActiveAgentSession(ctx, identity.ID)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if session.CurrentExecutionID != "" || session.LastExecutionID == "" || session.ExecutionCount != 1 {
		cancel()
		t.Fatalf("unexpected agent session after execution: %+v", session)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wakeup worker did not stop after cancellation")
	}
	var handled int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=? AND event_type='task.assigned' AND status='handled'", identity.ID).Scan(&handled); err != nil {
		t.Fatal(err)
	}
	if handled != 1 {
		t.Fatalf("expected one handled assignment event, got %d", handled)
	}
}
