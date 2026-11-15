package collaboration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/collaboration"
)

func TestAgentIdentityLifecycleEnableDisableDeleteRecover(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{
		Name: "Agent Lifecycle", Slug: "agent-lifecycle", AutonomyMode: "autonomous",
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := service.CreateAgent(ctx, collaboration.AgentIdentity{
		WorkspaceID: workspace.ID, Name: "Lifecycle Worker", Capabilities: []string{"test.execute"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Status != "offline" {
		t.Fatalf("new Agent should start offline, got %q", agent.Status)
	}

	enabled, err := service.EnableAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if enabled.Status != "idle" || enabled.DeletedAt != nil {
		t.Fatalf("unexpected enabled Agent: %+v", enabled)
	}

	task, err := service.CreateTask(ctx, collaboration.Task{
		WorkspaceID: workspace.ID, Title: "Lifecycle lease guard", RequiredCapabilities: []string{"test.execute"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := service.AssignTask(ctx, task.ID, agent.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimAssignment(ctx, assignment.ID, "lifecycle-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.DisableAgent(ctx, agent.ID); !errors.Is(err, collaboration.ErrAgentBusy) {
		t.Fatalf("disable must reject an active lease, got %v", err)
	}
	if _, err = service.DeleteAgent(ctx, agent.ID); !errors.Is(err, collaboration.ErrAgentBusy) {
		t.Fatalf("delete must reject an active lease, got %v", err)
	}
	if err = service.CompleteAssignment(ctx, assignment.ID, "lifecycle-worker", claimed.ClaimVersion); err != nil {
		t.Fatal(err)
	}

	disabled, err := service.DisableAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Status != "disabled" {
		t.Fatalf("expected disabled status, got %q", disabled.Status)
	}
	disabledTask, err := service.CreateTask(ctx, collaboration.Task{WorkspaceID: workspace.ID, Title: "Must not be assigned while disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AssignTask(ctx, disabledTask.ID, agent.ID, "test"); !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("disabled Agent must not receive assignments, got %v", err)
	}

	deleted, err := service.DeleteAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.DeletedAt == nil || deleted.Status != "disabled" {
		t.Fatalf("delete should be a recoverable soft-delete, got %+v", deleted)
	}
	listed, err := service.ListAgents(ctx, workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range listed {
		if item.ID == agent.ID {
			t.Fatal("soft-deleted Agent must be omitted from active Agent listings")
		}
	}
	if _, err = service.EnableAgent(ctx, agent.ID); !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("deleted Agent must not be enabled before recovery, got %v", err)
	}

	recovered, err := service.RecoverAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.DeletedAt != nil || recovered.Status != "idle" {
		t.Fatalf("unexpected recovered Agent: %+v", recovered)
	}
	recoveredTask, err := service.CreateTask(ctx, collaboration.Task{WorkspaceID: workspace.ID, Title: "Can be assigned after recovery"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AssignTask(ctx, recoveredTask.ID, agent.ID, "test"); err != nil {
		t.Fatalf("recovered Agent should accept assignments: %v", err)
	}
	if _, err = service.RecoverAgent(ctx, agent.ID); !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("recovering an active Agent twice must conflict, got %v", err)
	}
}
