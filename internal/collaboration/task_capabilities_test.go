package collaboration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/thkx/agentmesh/internal/collaboration"
)

func TestAssignTaskEnforcesRequiredCapabilities(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Capability Routing", Slug: "capability-routing"})
	if err != nil {
		t.Fatal(err)
	}
	researcher, err := service.CreateAgent(ctx, collaboration.AgentIdentity{
		WorkspaceID: workspace.ID, Name: "Researcher", Capabilities: []string{"research", "web.search"},
	})
	if err != nil {
		t.Fatal(err)
	}
	coder, err := service.CreateAgent(ctx, collaboration.AgentIdentity{
		WorkspaceID: workspace.ID, Name: "Coder", Capabilities: []string{"code.write", "test.run"},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, collaboration.Task{
		WorkspaceID: workspace.ID, Title: "Implement feature",
		RequiredCapabilities: []string{"test.run", "code.write", "code.write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AssignTask(ctx, task.ID, researcher.ID, "human"); !errors.Is(err, collaboration.ErrCapabilityMismatch) {
		t.Fatalf("agent without required capabilities must be rejected, got %v", err)
	}
	assignment, err := service.AssignTask(ctx, task.ID, coder.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	if assignment.AgentID != coder.ID {
		t.Fatalf("task assigned to wrong Agent: %+v", assignment)
	}
	stored, err := service.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.RequiredCapabilities) != 2 || stored.RequiredCapabilities[0] != "code.write" || stored.RequiredCapabilities[1] != "test.run" {
		t.Fatalf("required capabilities were not normalized: %+v", stored.RequiredCapabilities)
	}
}

func TestHandoffRejectsRecipientWithoutRequiredCapabilities(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Handoff Capability", Slug: "handoff-capability"})
	if err != nil {
		t.Fatal(err)
	}
	requester, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Lead", Capabilities: []string{"delegate"}})
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Analyst", Capabilities: []string{"research"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := service.CreateTask(ctx, collaboration.Task{WorkspaceID: workspace.ID, Title: "Parent task", CreatedBy: requester.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.HandoffTask(ctx, parent.ID, collaboration.Handoff{
		RequesterAgentID: requester.ID, Goal: "Build a tested implementation", Recipient: recipient.ID,
		RequiredCapabilities: []string{"code.write", "test.run"}, IdempotencyKey: "capability-handoff-1",
	}, requester.ID)
	if !errors.Is(err, collaboration.ErrCapabilityMismatch) {
		t.Fatalf("handoff to an incapable Agent must fail before approval, got %v", err)
	}
}
