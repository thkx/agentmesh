package collaboration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/collaboration"
	"github.com/thkx/agentmesh/internal/config"
	"github.com/thkx/agentmesh/internal/conversation"
	execdom "github.com/thkx/agentmesh/internal/domain/execution"
	executionrepo "github.com/thkx/agentmesh/internal/execution/repository"
	"github.com/thkx/agentmesh/internal/platform/database"
)

func testService(t *testing.T) (*collaboration.Service, *database.SQLite) {
	t.Helper()
	cfg := config.Defaults().Database
	cfg.DSN = filepath.Join(t.TempDir(), "collaboration.db")
	db, err := database.OpenSQLite(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.RunMigrations(db.DB); err != nil {
		db.Close()
		t.Fatal(err)
	}
	cr, err := conversation.NewSQLiteRepository(db.DB)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	cs, err := conversation.NewService(cr)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	s, err := collaboration.NewService(db.DB, cs)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return s, db
}

func TestCollaborationWorkspaceIdentityTaskAndLease(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Core Team", Slug: "core-team"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Planner", Role: "planner", Capabilities: []string{"research"}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Review architecture", Type: "analysis"})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := s.AssignTask(ctx, task.ID, a.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimAssignment(ctx, assignment.ID, "runner-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ClaimVersion != 1 || claimed.Status != "claimed" {
		t.Fatalf("unexpected claim: %+v", claimed)
	}
	if err = s.CompleteAssignment(ctx, claimed.ID, "wrong-runner", claimed.ClaimVersion); err != collaboration.ErrLeaseLost {
		t.Fatalf("expected fenced completion, got %v", err)
	}
	renewed, err := s.RenewAssignment(ctx, claimed.ID, "runner-1", claimed.ClaimVersion, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ClaimVersion != claimed.ClaimVersion {
		t.Fatal("lease renewal must preserve fencing version")
	}
	if err = s.CompleteAssignment(ctx, claimed.ID, "runner-1", claimed.ClaimVersion); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceConcurrencyLimitIsEnforcedAtClaim(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Capacity", Slug: "capacity", MaxAgentConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	a1, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Worker One", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Worker Two", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "First"})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	as1, err := s.AssignTask(ctx, t1.ID, a1.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	as2, err := s.AssignTask(ctx, t2.ID, a2.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimAssignment(ctx, as1.ID, "runner-one", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimAssignment(ctx, as2.ID, "runner-two", time.Minute); !errors.Is(err, collaboration.ErrWorkspaceConcurrencyLimit) {
		t.Fatalf("expected workspace concurrency limit, got %v", err)
	}
	if err = s.CompleteAssignment(ctx, claimed.ID, "runner-one", claimed.ClaimVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimAssignment(ctx, as2.ID, "runner-two", time.Minute); err != nil {
		t.Fatalf("claim should succeed after capacity is released: %v", err)
	}
}

func TestAgentCannotClaimMultipleConcurrentAssignments(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Single Agent", Slug: "single-agent", MaxAgentConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Serial Worker", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "First"})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	as1, err := s.AssignTask(ctx, t1.ID, a.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	as2, err := s.AssignTask(ctx, t2.ID, a.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimAssignment(ctx, as1.ID, "runner-one", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimAssignment(ctx, as2.ID, "runner-two", time.Minute); !errors.Is(err, collaboration.ErrAgentBusy) {
		t.Fatalf("expected active agent guard, got %v", err)
	}
	if err = s.CompleteAssignment(ctx, claimed.ID, "runner-one", claimed.ClaimVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimAssignment(ctx, as2.ID, "runner-two", time.Minute); err != nil {
		t.Fatalf("agent should be claimable after its active assignment completes: %v", err)
	}
}

func TestOutboxDispatchUsesExplicitRecipientsOnly(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Ops", Slug: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Target", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Other", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Assigned work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AssignTask(ctx, task.ID, target.ID, "human"); err != nil {
		t.Fatal(err)
	}

	d, err := collaboration.NewDispatcher(db.DB, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.DispatchOnce(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if err = d.DispatchOnce(ctx, 100); err != nil {
		t.Fatal(err)
	}

	var targetCount, otherCount, pending int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=?", target.ID).Scan(&targetCount); err != nil {
		t.Fatal(err)
	}
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=?", other.ID).Scan(&otherCount); err != nil {
		t.Fatal(err)
	}
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_outbox WHERE published_at IS NULL").Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if targetCount != 1 {
		t.Fatalf("expected only the assigned agent to receive one event, got %d", targetCount)
	}
	if otherCount != 0 {
		t.Fatalf("unknown events must not be workspace-broadcast, got %d inbox events", otherCount)
	}
	if pending != 0 {
		t.Fatalf("expected outbox to be drained, got %d pending", pending)
	}
	var unroutable int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_outbox WHERE published_at IS NOT NULL AND last_error LIKE 'unroutable:%'").Scan(&unroutable); err != nil {
		t.Fatal(err)
	}
	if unroutable == 0 {
		t.Fatal("expected unsupported events to be recorded as unroutable")
	}
}

const maxOutboxTestRetries = 8

func TestOutboxPoisonEventDoesNotBlockAndEventuallyDeadLetters(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Outbox Retry", Slug: "outbox-retry"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Worker", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.DB.ExecContext(ctx, `INSERT INTO collaboration_outbox(id,workspace_id,aggregate_type,aggregate_id,event_type,payload_json,created_at)
 VALUES('poison-event',?,'task','bad-task','task.assigned','[]',1)`, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Valid task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AssignTask(ctx, task.ID, a.ID, "human"); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := collaboration.NewDispatcher(db.DB, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = dispatcher.DispatchOnce(ctx, 100); err == nil {
		t.Fatal("expected malformed event dispatch error")
	}
	var inboxCount, attempts int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=?", a.ID).Scan(&inboxCount); err != nil {
		t.Fatal(err)
	}
	if inboxCount != 1 {
		t.Fatalf("valid event behind poison event was not delivered: inbox=%d", inboxCount)
	}
	if err = db.DB.QueryRowContext(ctx, "SELECT attempts FROM collaboration_outbox WHERE id='poison-event'").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("expected first retry attempt, got %d", attempts)
	}
	for i := 1; i < maxOutboxTestRetries; i++ {
		if _, err = db.DB.ExecContext(ctx, "UPDATE collaboration_outbox SET next_attempt_at=0 WHERE id='poison-event'"); err != nil {
			t.Fatal(err)
		}
		_ = dispatcher.DispatchOnce(ctx, 100)
	}
	var published sql.NullInt64
	var lastError string
	if err = db.DB.QueryRowContext(ctx, "SELECT published_at,last_error,attempts FROM collaboration_outbox WHERE id='poison-event'").Scan(&published, &lastError, &attempts); err != nil {
		t.Fatal(err)
	}
	if !published.Valid || !strings.HasPrefix(lastError, "dead-letter:") || attempts != maxOutboxTestRetries {
		t.Fatalf("poison event was not dead-lettered: published=%v error=%q attempts=%d", published, lastError, attempts)
	}
	deadLetters, err := s.ListDeadLetters(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(deadLetters) != 1 || deadLetters[0].ID != "poison-event" || deadLetters[0].Attempts != maxOutboxTestRetries {
		t.Fatalf("dead-letter inspection did not expose poison event: %+v", deadLetters)
	}
	if _, err = db.DB.ExecContext(ctx, "UPDATE collaboration_outbox SET payload_json=? WHERE id='poison-event'", `{"agent_id":"`+a.ID+`"}`); err != nil {
		t.Fatal(err)
	}
	if err = s.RequeueDeadLetter(ctx, "poison-event"); err != nil {
		t.Fatal(err)
	}
	if err = dispatcher.DispatchOnce(ctx, 100); err != nil {
		t.Fatal(err)
	}
	deadLetters, err = s.ListDeadLetters(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(deadLetters) != 0 {
		t.Fatalf("requeued event should leave dead-letter queue: %+v", deadLetters)
	}
	published = sql.NullInt64{}
	lastError = ""
	if err = db.DB.QueryRowContext(ctx, "SELECT published_at,last_error FROM collaboration_outbox WHERE id='poison-event'").Scan(&published, &lastError); err != nil {
		t.Fatal(err)
	}
	if !published.Valid || lastError != "" {
		t.Fatalf("requeued event was not successfully published: published=%v error=%q", published, lastError)
	}

}

func TestMessageEventsRouteOnlyToChannelMembers(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Messages", Slug: "messages"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Member", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Outsider", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.CreateChannel(ctx, collaboration.Channel{WorkspaceID: w.ID, Name: "Build", Kind: "team", MemberIDs: []string{member.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PostMessage(ctx, ch.ID, "user", "operator", "ship it"); err != nil {
		t.Fatal(err)
	}
	d, err := collaboration.NewDispatcher(db.DB, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.DispatchOnce(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var memberCount, outsiderCount int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=?", member.ID).Scan(&memberCount); err != nil {
		t.Fatal(err)
	}
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=?", outsider.ID).Scan(&outsiderCount); err != nil {
		t.Fatal(err)
	}
	if memberCount != 1 {
		t.Fatalf("expected channel member to receive message, got %d", memberCount)
	}
	if outsiderCount != 0 {
		t.Fatalf("channel outsider received message: %d", outsiderCount)
	}
}

func TestDispatcherExpiresApprovalsAndNotifiesRequester(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Approval Expiry", Slug: "approval-expiry"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Requester", Role: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := s.EvaluateDecision(ctx, collaboration.DecisionRequest{AgentID: a.ID, Action: "delegate", ResourceType: "task", ResourceID: "task-expire", Reason: "test expiry"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.ApprovalID == "" {
		t.Fatal("expected approval id")
	}
	if _, err = db.DB.ExecContext(ctx, "UPDATE collaboration_approvals SET expires_at=? WHERE id=?", time.Now().Add(-time.Minute).UnixMilli(), decision.ApprovalID); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := collaboration.NewDispatcher(db.DB, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = dispatcher.DispatchOnce(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.DB.QueryRowContext(ctx, "SELECT status FROM collaboration_approvals WHERE id=?", decision.ApprovalID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "expired" {
		t.Fatalf("approval status=%q", status)
	}
	var notified int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=? AND event_type='approval.expired'", a.ID).Scan(&notified); err != nil {
		t.Fatal(err)
	}
	if notified != 1 {
		t.Fatalf("requester should receive one expiry event, got %d", notified)
	}
}

func TestInboxReceiptIsIdempotentPerAgent(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Inbox", Slug: "inbox"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Inbox Agent", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	event := collaboration.Event{ID: "event-1", Type: "test.event", Payload: []byte("{\"ok\":true}")}
	first, err := s.ReceiveEvent(ctx, a.ID, event)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ReceiveEvent(ctx, a.ID, event)
	if err != nil {
		t.Fatal(err)
	}
	if !first || second {
		t.Fatalf("expected first delivery only, got %v then %v", first, second)
	}
	items, err := s.ListInbox(ctx, a.ID, "pending", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one pending inbox event, got %d", len(items))
	}
	if err = s.AckInbox(ctx, a.ID, event.ID, "handled"); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionGateRejectsActionsWithoutExecutionContracts(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Decision Contract", Slug: "decision-contract", AutonomyMode: "autonomous"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Agent", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"execute_workflow", "direct_execute"} {
		if _, err = s.EvaluateDecision(ctx, collaboration.DecisionRequest{AgentID: a.ID, Action: action, ResourceType: "workflow", ResourceID: "workflow-1"}); !errors.Is(err, collaboration.ErrInvalid) {
			t.Fatalf("unsupported action %q must not create an orphan approval: %v", action, err)
		}
	}
	var approvals int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_approvals WHERE workspace_id=?", w.ID).Scan(&approvals); err != nil {
		t.Fatal(err)
	}
	if approvals != 0 {
		t.Fatalf("unsupported actions created %d approvals", approvals)
	}
}

func TestDecisionGateRequiresApprovalForPrivilegedActions(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Governed", Slug: "governed", AutonomyMode: "hybrid"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Delegate", Role: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := s.EvaluateDecision(ctx, collaboration.DecisionRequest{AgentID: a.ID, Action: "delegate", ResourceType: "task", ResourceID: "task-1", Reason: "split research"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != "approval_required" || decision.ApprovalID == "" {
		t.Fatalf("expected approval gate, got %+v", decision)
	}
	approvals, err := s.ListApprovals(ctx, w.ID, "pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != 1 || approvals[0].ID != decision.ApprovalID {
		t.Fatalf("unexpected approvals: %+v", approvals)
	}
	resolved, err := s.ResolveApproval(ctx, decision.ApprovalID, "human-reviewer", "approved", "scope is safe")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "approved" {
		t.Fatalf("expected approved, got %s", resolved.Status)
	}
	allowed, err := s.EvaluateDecision(ctx, collaboration.DecisionRequest{AgentID: a.ID, Action: "claim"})
	if err != nil {
		t.Fatal(err)
	}
	if allowed.Outcome != "allowed" {
		t.Fatalf("claim should be allowed in hybrid mode: %+v", allowed)
	}
}

func TestHandoffApprovalIsBoundToExactRequest(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Scoped Approval", Slug: "scoped-approval", AutonomyMode: "hybrid"})
	if err != nil {
		t.Fatal(err)
	}
	planner, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Planner", Role: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Worker", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	first := collaboration.Handoff{RequesterAgentID: planner.ID, Goal: "Collect public sources", Recipient: worker.ID, IdempotencyKey: "handoff-a"}
	_, _, err = s.HandoffTask(ctx, parent.ID, first, planner.ID)
	var approvalErr *collaboration.ApprovalRequiredError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("expected first approval, got %v", err)
	}
	firstApproval := approvalErr.ApprovalID
	if _, err = s.ResolveApproval(ctx, firstApproval, "human", "approved", "approved exact request"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.HandoffTask(ctx, parent.ID, first, planner.ID); err != nil {
		t.Fatalf("approved request should be allowed on retry: %v", err)
	}
	second := collaboration.Handoff{RequesterAgentID: planner.ID, Goal: "Write implementation", Recipient: worker.ID, IdempotencyKey: "handoff-b"}
	_, _, err = s.HandoffTask(ctx, parent.ID, second, planner.ID)
	var secondApproval *collaboration.ApprovalRequiredError
	if !errors.As(err, &secondApproval) {
		t.Fatalf("different handoff should require a new approval, got %v", err)
	}
	if secondApproval.ApprovalID == firstApproval {
		t.Fatal("approval for one handoff was reused for a different request")
	}
}

func TestHandoffCreatesChildAndAssignmentAtomically(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Atomic Handoff", Slug: "atomic-handoff", AutonomyMode: "autonomous"})
	if err != nil {
		t.Fatal(err)
	}
	planner, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Planner", Role: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Worker", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Parent task"})
	if err != nil {
		t.Fatal(err)
	}
	handoff := collaboration.Handoff{RequesterAgentID: planner.ID, Goal: "Child task", Recipient: worker.ID, IdempotencyKey: "atomic-child", Reason: "split work"}
	if _, err = db.DB.ExecContext(ctx, `CREATE TRIGGER reject_handoff_assignment BEFORE INSERT ON task_assignments
  WHEN NEW.status='pending' BEGIN SELECT RAISE(ABORT,'forced assignment failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.HandoffTask(ctx, parent.ID, handoff, planner.ID); err == nil {
		t.Fatal("expected assignment insert failure")
	}
	var children, childEvents int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_tasks WHERE parent_task_id=?", parent.ID).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_outbox WHERE event_type='task.created' AND aggregate_id IN (SELECT id FROM collaboration_tasks WHERE parent_task_id=?)", parent.ID).Scan(&childEvents); err != nil {
		t.Fatal(err)
	}
	if children != 0 || childEvents != 0 {
		t.Fatalf("handoff partially committed: children=%d child_events=%d", children, childEvents)
	}
	if _, err = db.DB.ExecContext(ctx, "DROP TRIGGER reject_handoff_assignment"); err != nil {
		t.Fatal(err)
	}
	child, assignment, err := s.HandoffTask(ctx, parent.ID, handoff, planner.ID)
	if err != nil {
		t.Fatal(err)
	}
	again, againAssignment, err := s.HandoffTask(ctx, parent.ID, handoff, planner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ID != again.ID || assignment.ID != againAssignment.ID {
		t.Fatal("handoff retry did not return the same child and assignment")
	}
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_tasks WHERE parent_task_id=?", parent.ID).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 1 {
		t.Fatalf("expected one idempotent child task, got %d", children)
	}
}

func TestPendingApprovalHasDatabaseLevelDeduplication(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Approval Dedupe", Slug: "approval-dedupe", AutonomyMode: "hybrid"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Planner", Role: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.EvaluateDecision(ctx, collaboration.DecisionRequest{AgentID: a.ID, Action: "delegate", ResourceType: "task", ResourceID: "task-dedupe", Reason: "dedupe"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.EvaluateDecision(ctx, collaboration.DecisionRequest{AgentID: a.ID, Action: "delegate", ResourceType: "task", ResourceID: "task-dedupe", Reason: "dedupe"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ApprovalID == "" || second.ApprovalID != first.ApprovalID {
		t.Fatalf("pending approval was not reused: first=%q second=%q", first.ApprovalID, second.ApprovalID)
	}
	var count int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_approvals WHERE workspace_id=? AND requester_agent_id=? AND resource_type='task' AND resource_id='task-dedupe' AND action='delegate' AND status='pending'", w.ID, a.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one pending approval, got %d", count)
	}
}

func TestHandoffRequiresHumanApprovalInHybridMode(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Hybrid", Slug: "hybrid", AutonomyMode: "hybrid"})
	if err != nil {
		t.Fatal(err)
	}
	planner, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Planner", Role: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Worker", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Research", Type: "research"})
	if err != nil {
		t.Fatal(err)
	}
	handoff := collaboration.Handoff{RequesterAgentID: planner.ID, Goal: "Collect sources", Recipient: worker.ID, IdempotencyKey: "approval-handoff"}
	_, _, err = s.HandoffTask(ctx, parent.ID, handoff, planner.ID)
	var approvalErr *collaboration.ApprovalRequiredError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("expected approval required, got %v", err)
	}
	if _, _, err = s.HandoffTask(ctx, parent.ID, handoff, planner.ID); !errors.As(err, &approvalErr) {
		t.Fatalf("expected same pending approval on retry, got %v", err)
	}
	if _, err = s.ResolveApproval(ctx, approvalErr.ApprovalID, "human", "approved", "safe delegation"); err != nil {
		t.Fatal(err)
	}
	child, assignment, err := s.HandoffTask(ctx, parent.ID, handoff, planner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentTaskID != parent.ID || assignment.AgentID != worker.ID {
		t.Fatalf("handoff did not complete after approval: %+v %+v", child, assignment)
	}
}

func TestPostMessageAndOutboxAreAtomic(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Atomic", Slug: "atomic"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Member", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.CreateChannel(ctx, collaboration.Channel{WorkspaceID: w.ID, Name: "Atomic Channel", Kind: "team", MemberIDs: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.DB.ExecContext(ctx, `CREATE TRIGGER reject_message_outbox BEFORE INSERT ON collaboration_outbox
		WHEN NEW.event_type='message.created' BEGIN SELECT RAISE(ABORT,'forced outbox failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PostMessage(ctx, ch.ID, "user", "operator", "must roll back"); err == nil {
		t.Fatal("expected outbox failure")
	}
	messages, err := s.Messages(ctx, ch.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("message persisted without its outbox event: %d", len(messages))
	}
}

func TestTaskResultHumanReviewAuditIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Review Audit", Slug: "review-audit"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Review me"})
	if err != nil {
		t.Fatal(err)
	}
	as, err := s.AssignTask(ctx, task.ID, a.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.ExecContext(ctx, "UPDATE task_assignments SET status='completed' WHERE id=?", as.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.StoreTaskResult(ctx, a.ID, task.ID, as.ID, "exec-review", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	result, err := s.GetTaskResult(ctx, a.ID, as.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StoreTaskResultAssessment(ctx, collaboration.TaskResultAssessment{ResultID: result.ID, RecipientAgentID: a.ID, Outcome: "human_review", Reason: "needs review", Evidence: []byte("[]")}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ResolveTaskResultAssessment(ctx, a.ID, as.ID, "human", "accepted", "verified")
	if err != nil {
		t.Fatal(err)
	}
	if got.ReviewerID != "human" || got.Outcome != "accepted" || got.ReviewedAt == nil {
		t.Fatalf("unexpected resolution: %+v", got)
	}
	if _, err = s.ResolveTaskResultAssessment(ctx, a.ID, as.ID, "human", "accepted", "verified"); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if _, err = s.ResolveTaskResultAssessment(ctx, a.ID, as.ID, "other", "rejected", "different"); !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	audit, err := s.ListTaskResultReviewAudit(ctx, a.ID, as.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 1 || audit[0].Outcome != "accepted" {
		t.Fatalf("audit: %+v", audit)
	}
}

func TestAssignmentFailureAndRetryAreBounded(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Retry", Slug: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Worker"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Retryable"})
	if err != nil {
		t.Fatal(err)
	}
	as, err := s.AssignTask(ctx, task.ID, a.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimAssignment(ctx, as.ID, "runner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FailAssignmentWithRetry(ctx, claimed.ID, "runner", claimed.ClaimVersion, "temporary", true); err != nil {
		t.Fatal(err)
	}
	got, err := s.RetryAssignment(ctx, as.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" {
		t.Fatalf("status=%s", got.Status)
	}
	dispatcher, err := collaboration.NewDispatcher(db.DB, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = dispatcher.DispatchOnce(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var retryInboxCount int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=? AND event_type='task.assignment_retried'", a.ID).Scan(&retryInboxCount); err != nil {
		t.Fatal(err)
	}
	if retryInboxCount != 1 {
		t.Fatalf("assignment retry event was not routed to its agent: %d", retryInboxCount)
	}
	for i := 0; i < 3; i++ {
		claimed, err = s.ClaimAssignment(ctx, as.ID, "runner", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.FailAssignmentWithRetry(ctx, claimed.ID, "runner", claimed.ClaimVersion, "again", true); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if _, err = s.RetryAssignment(ctx, as.ID, "operator"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = s.RetryAssignment(ctx, as.ID, "operator"); !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("retry limit: %v", err)
	}
}

func TestRetryAssignmentRejectsPriorToolSideEffects(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Unsafe Retry", Slug: "unsafe-retry"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Worker"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Do not replay side effects"})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := s.AssignTask(ctx, task.ID, a.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimAssignment(ctx, assignment.ID, "retry-safety-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FailAssignmentWithRetry(ctx, claimed.ID, "retry-safety-test", claimed.ClaimVersion, "runtime failed", true); err != nil {
		t.Fatal(err)
	}
	runtimeTaskID := "collab-" + assignment.ID
	now := time.Now().UnixNano()
	if _, err = db.DB.ExecContext(ctx, `INSERT INTO agent_jobs
		(id,task_id,task_json,status,attempts,max_attempts,available_at,created_at,updated_at)
		VALUES(?,?,?,'failed',1,3,?,?,?)`, "unsafe-retry-job", runtimeTaskID, `{"id":"`+runtimeTaskID+`"}`, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.ExecContext(ctx, `INSERT INTO agent_tool_invocations
		(idempotency_key,task_id,tool_call_id,tool_name,status,created_at,updated_at)
		VALUES(?,?,? ,?,'started',?,?)`, "unsafe-retry-tool", runtimeTaskID, "call-1", "file.write", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RetryAssignment(ctx, assignment.ID, "operator"); !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("retry must reject prior tool side effects, got %v", err)
	}
	var status string
	if err = db.DB.QueryRowContext(ctx, "SELECT status FROM task_assignments WHERE id=?", assignment.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "expired" {
		t.Fatalf("unsafe retry mutated assignment status: %s", status)
	}
}

func TestParentTaskProgressRequiresParentAgentAcceptance(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Progress", Slug: "progress"})
	if err != nil {
		t.Fatal(err)
	}
	pa, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	ca, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Child"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AssignTask(ctx, parent.ID, pa.ID, "test"); err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, ParentTaskID: parent.ID, Title: "Child"})
	if err != nil {
		t.Fatal(err)
	}
	as, err := s.AssignTask(ctx, child.ID, ca.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.ExecContext(ctx, "UPDATE task_assignments SET status='completed' WHERE id=?", as.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.ExecContext(ctx, "UPDATE collaboration_tasks SET status='completed' WHERE id=?", child.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.StoreTaskResult(ctx, pa.ID, child.ID, as.ID, "exec-parent", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetTaskChildProgress(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.ReadyForParentReview {
		t.Fatal("unreviewed result cannot pass")
	}
	res, err := s.GetTaskResult(ctx, pa.ID, as.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StoreTaskResultAssessment(ctx, collaboration.TaskResultAssessment{ResultID: res.ID, RecipientAgentID: pa.ID, Outcome: "human_review", Reason: "verify", Evidence: []byte("[]")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveTaskResultAssessment(ctx, pa.ID, as.ID, "human", "accepted", "verified"); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetTaskChildProgress(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReadyForParentReview {
		t.Fatalf("progress not ready: %+v", p)
	}
}

func TestTaskResultReviewAuditIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()

	workspace, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Immutable Audit", Slug: "immutable-audit"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: workspace.ID, Title: "Audited task"})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := s.AssignTask(ctx, task.ID, agent.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.ExecContext(ctx, "UPDATE task_assignments SET status='completed' WHERE id=?", assignment.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.StoreTaskResult(ctx, agent.ID, task.ID, assignment.ID, "audit-execution", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	result, err := s.GetTaskResult(ctx, agent.ID, assignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StoreTaskResultAssessment(ctx, collaboration.TaskResultAssessment{
		ResultID: result.ID, RecipientAgentID: agent.ID, Outcome: "human_review",
		Reason: "review required", Evidence: []byte("[]"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveTaskResultAssessment(ctx, agent.ID, assignment.ID, "human", "accepted", "verified"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.ExecContext(ctx, "UPDATE agent_task_result_review_audit SET note='tampered' WHERE assignment_id=?", assignment.ID); err == nil {
		t.Fatal("review audit update must be rejected")
	}
	if _, err = db.DB.ExecContext(ctx, "DELETE FROM agent_task_result_review_audit WHERE assignment_id=?", assignment.ID); err == nil {
		t.Fatal("review audit deletion must be rejected")
	}
}

func TestArtifactVersionEvidenceAndDeterministicVerification(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()

	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Artifacts", Slug: "artifacts"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Builder"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Build deliverable"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, version, err := s.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		WorkspaceID: w.ID, SourceTaskID: task.ID, CreatorAgentID: agent.ID,
		Kind: "document", Title: "Architecture report", Content: []byte("# Architecture"),
		MediaType: "text/markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.CurrentVersion != 1 || version.Version != 1 || len(version.ContentSHA256) != 64 {
		t.Fatalf("unexpected artifact: %+v %+v", artifact, version)
	}

	policy := collaboration.ArtifactVerificationPolicy{
		RequiredEvidenceKinds: []string{"test", "source_reference"},
		RequireContentDigest:  true,
	}
	check, err := s.VerifyArtifact(ctx, artifact.ID, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	if check.Outcome != "needs_evidence" {
		t.Fatalf("expected missing evidence, got %+v", check)
	}
	_, err = s.AddArtifactEvidence(ctx, artifact.ID, collaboration.AddArtifactEvidenceRequest{
		VersionID: version.ID, Kind: "test", ContentSHA256: "bad", Statement: "tests passed",
	})
	if !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("expected mismatched digest rejection, got %v", err)
	}
	for _, kind := range []string{"test", "source_reference"} {
		_, err = s.AddArtifactEvidence(ctx, artifact.ID, collaboration.AddArtifactEvidenceRequest{
			VersionID: version.ID, Kind: kind, ContentSHA256: version.ContentSHA256,
			Statement: "verified", CreatedBy: "operator",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check, err = s.VerifyArtifact(ctx, artifact.ID, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	if check.Outcome != "passed" {
		t.Fatalf("expected passed deterministic verification, got %+v", check)
	}

	newVersion, err := s.AddArtifactVersion(ctx, artifact.ID, collaboration.CreateArtifactRequest{
		Content: []byte("# Architecture v2"), MediaType: "text/markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if newVersion.Version != 2 {
		t.Fatalf("expected version 2, got %d", newVersion.Version)
	}
	oldVersion, err := s.GetArtifactVersion(ctx, artifact.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(oldVersion.Content) != "# Architecture" {
		t.Fatalf("old version changed: %q", oldVersion.Content)
	}
	latest, err := s.GetArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.CurrentVersion != 2 {
		t.Fatalf("current version=%d", latest.CurrentVersion)
	}
	evidence, err := s.ListArtifactEvidence(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 {
		t.Fatalf("expected 2 evidence records, got %d", len(evidence))
	}
	if _, err = db.DB.ExecContext(ctx, "UPDATE collaboration_artifact_versions SET media_type='text/plain' WHERE id=?", version.ID); err == nil {
		t.Fatal("artifact versions must be immutable")
	}
	if _, err = db.DB.ExecContext(ctx, "DELETE FROM collaboration_artifact_evidence WHERE id=?", evidence[0].ID); err == nil {
		t.Fatal("artifact evidence must be append-only")
	}
}

func TestArtifactRejectsCrossWorkspaceAndOversizedContent(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()

	w1, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "One", Slug: "one"})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Two", Slug: "two"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w2.ID, Title: "Foreign task"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		WorkspaceID: w1.ID, SourceTaskID: task.ID, Kind: "document", Title: "Invalid", Content: []byte("x"),
	})
	if !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("expected workspace conflict, got %v", err)
	}
	_, _, err = s.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		WorkspaceID: w1.ID, Kind: "document", Title: "Too large",
		Content: make([]byte, collaboration.MaxArtifactBytes+1),
	})
	if !errors.Is(err, collaboration.ErrInvalid) {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

func TestArtifactSubmissionRequiresVerificationAndReviewIsImmutable(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()

	workspace, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Artifact Review", Slug: "artifact-review"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, version, err := s.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		WorkspaceID: workspace.ID, Kind: "document", Title: "Release notes",
		Content: []byte("# Release"), MediaType: "text/markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitArtifact(ctx, artifact.ID); !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("submission without a passed verification must fail, got %v", err)
	}
	verification, err := s.VerifyArtifact(ctx, artifact.ID, version.Version, collaboration.ArtifactVerificationPolicy{
		RequireContentDigest: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verification.Outcome != "passed" {
		t.Fatalf("expected digest verification to pass, got %+v", verification)
	}
	submitted, err := s.SubmitArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Status != "submitted" || submitted.SubmittedVersionID != version.ID {
		t.Fatalf("unexpected submitted artifact: %+v", submitted)
	}

	review, err := s.ReviewArtifact(ctx, artifact.ID, "human-reviewer", "accepted", "release approved")
	if err != nil {
		t.Fatal(err)
	}
	if review.VersionID != version.ID || review.Outcome != "accepted" {
		t.Fatalf("unexpected review: %+v", review)
	}
	retry, err := s.ReviewArtifact(ctx, artifact.ID, "human-reviewer", "accepted", "release approved")
	if err != nil {
		t.Fatalf("identical review retry should be idempotent: %v", err)
	}
	if retry.ID != review.ID {
		t.Fatalf("review retry created another record: %s != %s", retry.ID, review.ID)
	}
	if _, err = s.ReviewArtifact(ctx, artifact.ID, "other-reviewer", "rejected", "different decision"); !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("conflicting second review must fail, got %v", err)
	}
	accepted, err := s.GetArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != "accepted" {
		t.Fatalf("expected accepted status, got %s", accepted.Status)
	}

	next, err := s.AddArtifactVersion(ctx, artifact.ID, collaboration.CreateArtifactRequest{
		Content: []byte("# Release v2"), MediaType: "text/markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.GetArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Status != "draft" || draft.SubmittedVersionID != "" || next.Version != 2 {
		t.Fatalf("new version must return artifact to draft: %+v version=%+v", draft, next)
	}
	if _, err = s.SubmitArtifact(ctx, artifact.ID); !errors.Is(err, collaboration.ErrConflict) {
		t.Fatalf("new version requires its own verification, got %v", err)
	}
	if _, err = s.VerifyArtifact(ctx, artifact.ID, 2, collaboration.ArtifactVerificationPolicy{RequireContentDigest: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitArtifact(ctx, artifact.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReviewArtifact(ctx, artifact.ID, "human-reviewer", "rejected", "needs revision"); err != nil {
		t.Fatal(err)
	}
	final, err := s.GetArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "rejected" {
		t.Fatalf("expected rejected status, got %s", final.Status)
	}

	if _, err = db.DB.ExecContext(ctx, "UPDATE collaboration_artifact_reviews SET note='tampered' WHERE artifact_id=?", artifact.ID); err == nil {
		t.Fatal("artifact review audit must be append-only")
	}
	if _, err = db.DB.ExecContext(ctx, "DELETE FROM collaboration_artifact_reviews WHERE artifact_id=?", artifact.ID); err == nil {
		t.Fatal("artifact review audit must not be deletable")
	}
	if _, err = db.DB.ExecContext(ctx, "UPDATE collaboration_artifact_verifications SET reason='tampered' WHERE artifact_id=?", artifact.ID); err == nil {
		t.Fatal("artifact verifications must be append-only")
	}
}

func TestArtifactAndVerificationHistoryCanBeListed(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()

	workspace, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Artifact Index", Slug: "artifact-index"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := s.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		WorkspaceID: workspace.ID, Kind: "report", Title: "Research output", Content: []byte("report"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = s.VerifyArtifact(ctx, artifact.ID, 1, collaboration.ArtifactVerificationPolicy{RequireContentDigest: true}); err != nil {
			t.Fatal(err)
		}
	}
	artifacts, err := s.ListArtifacts(ctx, workspace.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != artifact.ID {
		t.Fatalf("unexpected artifact listing: %+v", artifacts)
	}
	history, err := s.ListArtifactVerifications(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Outcome != "passed" {
		t.Fatalf("unexpected verification history: %+v", history)
	}
}

func TestArtifactAndEvidencePreserveExecutionAttemptProvenance(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	store, err := executionrepo.NewSQLiteStore(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	execution := execdom.Entity{
		ID: "p6-artifact-execution", Kind: execdom.KindCoding, Runtime: execdom.RuntimeCoding,
		TaskID: "p6-artifact-task", Status: execdom.LifecycleQueued,
		Input: json.RawMessage(`{"task":"write code"}`), CreatedAt: now, UpdatedAt: now,
	}
	if _, err = store.Create(ctx, execution); err != nil {
		t.Fatal(err)
	}
	attempt := execdom.Attempt{
		ID: execution.ID + "-a-1", ExecutionID: execution.ID, Number: 1, Runtime: execution.Runtime,
		Status: execdom.AttemptRunning, Input: execution.Input, StartedAt: now,
	}
	if _, err = store.CreateAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Coding Evidence", Slug: "coding-evidence"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, version, err := s.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		ID: "p6-coding-artifact", VersionID: "p6-coding-version",
		WorkspaceID: workspace.ID, Kind: "code_patch", Title: "Coding patch", Content: []byte("diff --git a/a b/a"),
		MediaType: "text/x-diff", SourceExecution: execution.ID, SourceAttempt: attempt.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if version.SourceExecution != execution.ID || version.SourceAttempt != attempt.ID {
		t.Fatalf("artifact provenance mismatch: %+v", version)
	}
	artifactAgain, versionAgain, err := s.CreateArtifact(ctx, collaboration.CreateArtifactRequest{
		ID: "p6-coding-artifact", VersionID: "p6-coding-version",
		WorkspaceID: workspace.ID, Kind: "code_patch", Title: "Coding patch", Content: []byte("diff --git a/a b/a"),
		MediaType: "text/x-diff", SourceExecution: execution.ID, SourceAttempt: attempt.ID,
	})
	if err != nil || artifactAgain.ID != artifact.ID || versionAgain.ID != version.ID {
		t.Fatalf("artifact publication is not idempotent: artifact=%+v version=%+v err=%v", artifactAgain, versionAgain, err)
	}
	evidence, err := s.AddArtifactEvidence(ctx, artifact.ID, collaboration.AddArtifactEvidenceRequest{
		ID: "p6-coding-evidence", VersionID: version.ID, Kind: "diff", SourceRef: "coding-task:p6-artifact-task:diff",
		ContentSHA256: version.ContentSHA256, Statement: "patch generated",
		ExecutionID: execution.ID, AttemptID: attempt.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.ExecutionID != execution.ID || evidence.AttemptID != attempt.ID {
		t.Fatalf("evidence provenance mismatch: %+v", evidence)
	}
	evidenceAgain, err := s.AddArtifactEvidence(ctx, artifact.ID, collaboration.AddArtifactEvidenceRequest{
		ID: "p6-coding-evidence", VersionID: version.ID, Kind: "diff", SourceRef: "coding-task:p6-artifact-task:diff",
		ContentSHA256: version.ContentSHA256, Statement: "patch generated", CreatedBy: "agentmesh-coding-runtime",
		ExecutionID: execution.ID, AttemptID: attempt.ID,
	})
	if err != nil || evidenceAgain.ID != evidence.ID {
		t.Fatalf("evidence publication is not idempotent: %+v err=%v", evidenceAgain, err)
	}
	items, err := s.ListArtifactEvidence(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ExecutionID != execution.ID || items[0].AttemptID != attempt.ID {
		t.Fatalf("persisted evidence provenance mismatch: %+v", items)
	}
}

func TestTaskResultAssessmentPersistsReviewerModelProvenance(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Review Provenance", Slug: "review-provenance"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Reviewer", Role: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, collaboration.Task{WorkspaceID: workspace.ID, Title: "Review a plain result"})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := service.AssignTask(ctx, task.ID, agent.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimAssignment(ctx, assignment.ID, "review-provenance-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.CompleteAssignment(ctx, assignment.ID, "review-provenance-test", claimed.ClaimVersion); err != nil {
		t.Fatal(err)
	}
	if err = service.StoreTaskResult(ctx, agent.ID, task.ID, assignment.ID, "execution-review-provenance", json.RawMessage(`{"content":"completed"}`)); err != nil {
		t.Fatal(err)
	}
	result, err := service.GetTaskResult(ctx, agent.ID, assignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	assessment := collaboration.TaskResultAssessment{
		ResultID: result.ID, WorkspaceID: workspace.ID, TaskID: task.ID,
		AssignmentID: assignment.ID, RecipientAgentID: agent.ID,
		Outcome: "accepted", Reason: "The result satisfies the task.",
		Confidence: 0.96, Evidence: json.RawMessage(`["task:task","result:result"]`),
		ReviewMethod: "model", ReviewerModelID: "model-deepseek-reviewer",
		ReviewerProviderID: "provider-deepseek", ModelResponseID: "response-123",
		ModelInvoked: true, InputTokens: 1200, OutputTokens: 180,
	}
	if err = service.StoreTaskResultAssessment(ctx, assessment); err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetTaskResultAssessment(ctx, agent.ID, assignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReviewMethod != "model" || !stored.ModelInvoked ||
		stored.ReviewerModelID != assessment.ReviewerModelID ||
		stored.ReviewerProviderID != assessment.ReviewerProviderID ||
		stored.ModelResponseID != assessment.ModelResponseID ||
		stored.InputTokens != assessment.InputTokens || stored.OutputTokens != assessment.OutputTokens {
		t.Fatalf("reviewer model provenance was not persisted: %+v", stored)
	}
}
