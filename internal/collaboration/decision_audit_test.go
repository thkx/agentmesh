package collaboration_test

import (
	"context"
	"testing"

	"github.com/thkx/agentmesh/internal/collaboration"
)

func TestDecisionAuditCanBeRetrievedByAgent(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Decision Audit", Slug: "decision-audit"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Policy Agent", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := service.EvaluateDecision(ctx, collaboration.DecisionRequest{
		AgentID: agent.ID, EventID: "event-audit-1", Action: "reply",
		ResourceType: "message", ResourceID: "message-audit-1", Reason: "reply is non-privileged",
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != "allowed" {
		t.Fatalf("expected allowed decision, got %+v", decision)
	}
	items, err := service.ListDecisionAudit(ctx, agent.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one audit record, got %d", len(items))
	}
	item := items[0]
	if item.AgentID != agent.ID || item.EventID != "event-audit-1" ||
		item.Action != "reply" || item.Outcome != "allowed" || item.ResourceID != "message-audit-1" {
		t.Fatalf("unexpected decision audit record: %+v", item)
	}
}
