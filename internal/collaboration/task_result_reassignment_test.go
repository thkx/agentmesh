package collaboration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/collaboration"
)

func TestRejectedTaskResultBlocksTaskAndAllowsReassignment(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Rework Flow", Slug: "rework-flow"})
	if err != nil {
		t.Fatal(err)
	}
	parentAgent, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Parent", Role: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	firstWorker, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "First Worker", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	secondWorker, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Second Worker", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}

	task, err := service.CreateTask(ctx, collaboration.Task{
		WorkspaceID: workspace.ID, Title: "Implement and verify feature", CreatedBy: parentAgent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstAssignment, err := service.AssignTask(ctx, task.ID, firstWorker.ID, parentAgent.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimAssignment(ctx, firstAssignment.ID, "rework-test-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resultJSON := json.RawMessage(`{"summary":"implementation delivered","tests":"incomplete"}`)
	if err = service.CompleteAssignmentWithResult(ctx, firstAssignment.ID, "rework-test-worker", claimed.ClaimVersion, "rework-execution-1", resultJSON); err != nil {
		t.Fatal(err)
	}
	if err = service.StoreTaskResult(ctx, parentAgent.ID, task.ID, firstAssignment.ID, "rework-execution-1", resultJSON); err != nil {
		t.Fatal(err)
	}
	result, err := service.GetTaskResult(ctx, parentAgent.ID, firstAssignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.StoreTaskResultAssessment(ctx, collaboration.TaskResultAssessment{
		ResultID: result.ID, RecipientAgentID: parentAgent.ID, Outcome: "rejected",
		Reason: "test evidence is incomplete", Confidence: 0.96,
		Evidence: json.RawMessage(`["evidence-test"]`),
	}); err != nil {
		t.Fatal(err)
	}

	blocked, err := service.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != "blocked" {
		t.Fatalf("rejected result should block task for rework, got %q", blocked.Status)
	}
	secondAssignment, err := service.AssignTask(ctx, task.ID, secondWorker.ID, parentAgent.ID)
	if err != nil {
		t.Fatalf("blocked task should be assignable to a different Agent: %v", err)
	}
	if secondAssignment.Status != "pending" || secondAssignment.AgentID != secondWorker.ID {
		t.Fatalf("unexpected rework assignment: %+v", secondAssignment)
	}
	reopened, err := service.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != "assigned" {
		t.Fatalf("task should return to assigned after reassignment, got %q", reopened.Status)
	}

	// Retrying the same result assessment is idempotent and must not mutate
	// the new assignment or duplicate the original review record.
	if err = service.StoreTaskResultAssessment(ctx, collaboration.TaskResultAssessment{
		ResultID: result.ID, RecipientAgentID: parentAgent.ID, Outcome: "rejected",
		Reason: "test evidence is incomplete", Confidence: 0.96,
		Evidence: json.RawMessage(`["evidence-test"]`),
	}); err != nil {
		t.Fatal(err)
	}
}
