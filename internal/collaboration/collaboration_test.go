package collaboration_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/collaboration"
	"github.com/thkx/agentmesh/internal/config"
	"github.com/thkx/agentmesh/internal/conversation"
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
	if err != nil { t.Fatal(err) }
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Planner", Role: "planner", Capabilities: []string{"research"}})
	if err != nil { t.Fatal(err) }
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Review architecture", Type: "analysis"})
	if err != nil { t.Fatal(err) }
	assignment, err := s.AssignTask(ctx, task.ID, a.ID, "human")
	if err != nil { t.Fatal(err) }
	claimed, err := s.ClaimAssignment(ctx, assignment.ID, "runner-1", time.Minute)
	if err != nil { t.Fatal(err) }
	if claimed.ClaimVersion != 1 || claimed.Status != "claimed" { t.Fatalf("unexpected claim: %+v", claimed) }
	if err = s.CompleteAssignment(ctx, claimed.ID, "wrong-runner", claimed.ClaimVersion); err != collaboration.ErrLeaseLost { t.Fatalf("expected fenced completion, got %v", err) }
	renewed, err := s.RenewAssignment(ctx, claimed.ID, "runner-1", claimed.ClaimVersion, time.Minute)
	if err != nil { t.Fatal(err) }
	if renewed.ClaimVersion != claimed.ClaimVersion { t.Fatal("lease renewal must preserve fencing version") }
	if err = s.CompleteAssignment(ctx, claimed.ID, "runner-1", claimed.ClaimVersion); err != nil { t.Fatal(err) }
}

func TestWorkspaceConcurrencyLimitIsEnforcedAtClaim(t *testing.T) {
 ctx:=context.Background()
 s,db:=testService(t);defer db.Close()
 w,err:=s.CreateWorkspace(ctx,collaboration.Workspace{Name:"Capacity",Slug:"capacity",MaxAgentConcurrency:1});if err!=nil{t.Fatal(err)}
 a1,err:=s.CreateAgent(ctx,collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Worker One",Role:"worker"});if err!=nil{t.Fatal(err)}
 a2,err:=s.CreateAgent(ctx,collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Worker Two",Role:"worker"});if err!=nil{t.Fatal(err)}
 t1,err:=s.CreateTask(ctx,collaboration.Task{WorkspaceID:w.ID,Title:"First"});if err!=nil{t.Fatal(err)}
 t2,err:=s.CreateTask(ctx,collaboration.Task{WorkspaceID:w.ID,Title:"Second"});if err!=nil{t.Fatal(err)}
 as1,err:=s.AssignTask(ctx,t1.ID,a1.ID,"human");if err!=nil{t.Fatal(err)}
 as2,err:=s.AssignTask(ctx,t2.ID,a2.ID,"human");if err!=nil{t.Fatal(err)}
 claimed,err:=s.ClaimAssignment(ctx,as1.ID,"runner-one",time.Minute);if err!=nil{t.Fatal(err)}
 if _,err=s.ClaimAssignment(ctx,as2.ID,"runner-two",time.Minute);!errors.Is(err,collaboration.ErrWorkspaceConcurrencyLimit){t.Fatalf("expected workspace concurrency limit, got %v",err)}
 if err=s.CompleteAssignment(ctx,claimed.ID,"runner-one",claimed.ClaimVersion);err!=nil{t.Fatal(err)}
 if _,err=s.ClaimAssignment(ctx,as2.ID,"runner-two",time.Minute);err!=nil{t.Fatalf("claim should succeed after capacity is released: %v",err)}
}

func TestOutboxDispatchUsesExplicitRecipientsOnly(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Ops", Slug: "ops"})
	if err != nil { t.Fatal(err) }
	target, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Target", Role: "worker"})
	if err != nil { t.Fatal(err) }
	other, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Other", Role: "worker"})
	if err != nil { t.Fatal(err) }
	task, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Assigned work"})
	if err != nil { t.Fatal(err) }
	if _, err = s.AssignTask(ctx, task.ID, target.ID, "human"); err != nil { t.Fatal(err) }

	d, err := collaboration.NewDispatcher(db.DB, time.Millisecond)
	if err != nil { t.Fatal(err) }
	if err = d.DispatchOnce(ctx, 100); err != nil { t.Fatal(err) }
	if err = d.DispatchOnce(ctx, 100); err != nil { t.Fatal(err) }

	var targetCount, otherCount, pending int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=?", target.ID).Scan(&targetCount); err != nil { t.Fatal(err) }
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=?", other.ID).Scan(&otherCount); err != nil { t.Fatal(err) }
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_outbox WHERE published_at IS NULL").Scan(&pending); err != nil { t.Fatal(err) }
	if targetCount != 1 { t.Fatalf("expected only the assigned agent to receive one event, got %d", targetCount) }
	if otherCount != 0 { t.Fatalf("unknown events must not be workspace-broadcast, got %d inbox events", otherCount) }
	if pending != 0 { t.Fatalf("expected outbox to be drained, got %d pending", pending) }
	var unroutable int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_outbox WHERE published_at IS NOT NULL AND last_error LIKE 'unroutable:%'").Scan(&unroutable); err != nil { t.Fatal(err) }
	if unroutable == 0 { t.Fatal("expected unsupported events to be recorded as unroutable") }
}

func TestMessageEventsRouteOnlyToChannelMembers(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Messages", Slug: "messages"})
	if err != nil { t.Fatal(err) }
	member, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Member", Role: "worker"})
	if err != nil { t.Fatal(err) }
	outsider, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Outsider", Role: "worker"})
	if err != nil { t.Fatal(err) }
	ch, err := s.CreateChannel(ctx, collaboration.Channel{WorkspaceID: w.ID, Name: "Build", Kind: "team", MemberIDs: []string{member.ID}})
	if err != nil { t.Fatal(err) }
	if _, err = s.PostMessage(ctx, ch.ID, "user", "operator", "ship it"); err != nil { t.Fatal(err) }
	d, err := collaboration.NewDispatcher(db.DB, time.Millisecond)
	if err != nil { t.Fatal(err) }
	if err = d.DispatchOnce(ctx, 100); err != nil { t.Fatal(err) }
	var memberCount, outsiderCount int
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=?", member.ID).Scan(&memberCount); err != nil { t.Fatal(err) }
	if err = db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=?", outsider.ID).Scan(&outsiderCount); err != nil { t.Fatal(err) }
	if memberCount != 1 { t.Fatalf("expected channel member to receive message, got %d", memberCount) }
	if outsiderCount != 0 { t.Fatalf("channel outsider received message: %d", outsiderCount) }
}

func TestInboxReceiptIsIdempotentPerAgent(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Inbox", Slug: "inbox"})
	if err != nil { t.Fatal(err) }
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Inbox Agent", Role: "worker"})
	if err != nil { t.Fatal(err) }
	event := collaboration.Event{ID: "event-1", Type: "test.event", Payload: []byte("{\"ok\":true}")}
	first, err := s.ReceiveEvent(ctx, a.ID, event)
	if err != nil { t.Fatal(err) }
	second, err := s.ReceiveEvent(ctx, a.ID, event)
	if err != nil { t.Fatal(err) }
	if !first || second { t.Fatalf("expected first delivery only, got %v then %v", first, second) }
	items, err := s.ListInbox(ctx, a.ID, "pending", 20)
	if err != nil { t.Fatal(err) }
	if len(items) != 1 { t.Fatalf("expected one pending inbox event, got %d", len(items)) }
	if err = s.AckInbox(ctx, a.ID, event.ID, "handled"); err != nil { t.Fatal(err) }
}

func TestDecisionGateRequiresApprovalForPrivilegedActions(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Governed", Slug: "governed", AutonomyMode: "hybrid"})
	if err != nil { t.Fatal(err) }
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Delegate", Role: "planner"})
	if err != nil { t.Fatal(err) }
	decision, err := s.EvaluateDecision(ctx, collaboration.DecisionRequest{AgentID: a.ID, Action: "delegate", ResourceType: "task", ResourceID: "task-1", Reason: "split research"})
	if err != nil { t.Fatal(err) }
	if decision.Outcome != "approval_required" || decision.ApprovalID == "" { t.Fatalf("expected approval gate, got %+v", decision) }
	approvals, err := s.ListApprovals(ctx, w.ID, "pending", 10)
	if err != nil { t.Fatal(err) }
	if len(approvals) != 1 || approvals[0].ID != decision.ApprovalID { t.Fatalf("unexpected approvals: %+v", approvals) }
	resolved, err := s.ResolveApproval(ctx, decision.ApprovalID, "human-reviewer", "approved", "scope is safe")
	if err != nil { t.Fatal(err) }
	if resolved.Status != "approved" { t.Fatalf("expected approved, got %s", resolved.Status) }
	allowed, err := s.EvaluateDecision(ctx, collaboration.DecisionRequest{AgentID: a.ID, Action: "claim"})
	if err != nil { t.Fatal(err) }
	if allowed.Outcome != "allowed" { t.Fatalf("claim should be allowed in hybrid mode: %+v", allowed) }
}

func TestHandoffRequiresHumanApprovalInHybridMode(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name: "Hybrid", Slug: "hybrid", AutonomyMode: "hybrid"})
	if err != nil { t.Fatal(err) }
	planner, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Planner", Role: "planner"})
	if err != nil { t.Fatal(err) }
	worker, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: w.ID, Name: "Worker", Role: "worker"})
	if err != nil { t.Fatal(err) }
	parent, err := s.CreateTask(ctx, collaboration.Task{WorkspaceID: w.ID, Title: "Research", Type: "research"})
	if err != nil { t.Fatal(err) }
	handoff := collaboration.Handoff{RequesterAgentID: planner.ID, Goal: "Collect sources", Recipient: worker.ID, IdempotencyKey: "approval-handoff"}
	_, _, err = s.HandoffTask(ctx, parent.ID, handoff, planner.ID)
	var approvalErr *collaboration.ApprovalRequiredError
	if !errors.As(err, &approvalErr) { t.Fatalf("expected approval required, got %v", err) }
	if _, _, err = s.HandoffTask(ctx, parent.ID, handoff, planner.ID); !errors.As(err, &approvalErr) { t.Fatalf("expected same pending approval on retry, got %v", err) }
	if _, err = s.ResolveApproval(ctx, approvalErr.ApprovalID, "human", "approved", "safe delegation"); err != nil { t.Fatal(err) }
	child, assignment, err := s.HandoffTask(ctx, parent.ID, handoff, planner.ID)
	if err != nil { t.Fatal(err) }
	if child.ParentTaskID != parent.ID || assignment.AgentID != worker.ID { t.Fatalf("handoff did not complete after approval: %+v %+v", child, assignment) }
}

func TestPostMessageAndOutboxAreAtomic(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name:"Atomic",Slug:"atomic"})
	if err != nil { t.Fatal(err) }
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Member",Role:"worker"})
	if err != nil { t.Fatal(err) }
	ch, err := s.CreateChannel(ctx, collaboration.Channel{WorkspaceID:w.ID,Name:"Atomic Channel",Kind:"team",MemberIDs:[]string{a.ID}})
	if err != nil { t.Fatal(err) }
	_, err = db.DB.ExecContext(ctx,`CREATE TRIGGER reject_message_outbox BEFORE INSERT ON collaboration_outbox
		WHEN NEW.event_type='message.created' BEGIN SELECT RAISE(ABORT,'forced outbox failure'); END;`)
	if err != nil { t.Fatal(err) }
	if _, err = s.PostMessage(ctx,ch.ID,"user","operator","must roll back"); err == nil { t.Fatal("expected outbox failure") }
	messages, err := s.Messages(ctx,ch.ID,0,20)
	if err != nil { t.Fatal(err) }
	if len(messages)!=0 { t.Fatalf("message persisted without its outbox event: %d",len(messages)) }
}
