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

func testService(t *testing.T)(*collaboration.Service,*database.SQLite){
 t.Helper();cfg:=config.Defaults().Database;cfg.DSN=filepath.Join(t.TempDir(),"collaboration.db")
 db,err:=database.OpenSQLite(context.Background(),cfg);if err!=nil{t.Fatal(err)}
 if err=database.RunMigrations(db.DB);err!=nil{db.Close();t.Fatal(err)}
 cr,err:=conversation.NewSQLiteRepository(db.DB);if err!=nil{db.Close();t.Fatal(err)}
 cs,err:=conversation.NewService(cr);if err!=nil{db.Close();t.Fatal(err)}
 s,err:=collaboration.NewService(db.DB,cs);if err!=nil{db.Close();t.Fatal(err)}
 return s,db
}
func TestCollaborationWorkspaceIdentityTaskAndLease(t *testing.T){
 ctx:=context.Background();s,db:=testService(t);defer db.Close()
 w,err:=s.CreateWorkspace(ctx,collaboration.Workspace{Name:"Core Team",Slug:"core-team"});if err!=nil{t.Fatal(err)}
 a,err:=s.CreateAgent(ctx,collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Planner",Role:"planner",Capabilities:[]string{"research"}});if err!=nil{t.Fatal(err)}
 task,err:=s.CreateTask(ctx,collaboration.Task{WorkspaceID:w.ID,Title:"Review architecture",Type:"analysis"});if err!=nil{t.Fatal(err)}
 assignment,err:=s.AssignTask(ctx,task.ID,a.ID,"human");if err!=nil{t.Fatal(err)}
 claimed,err:=s.ClaimAssignment(ctx,assignment.ID,"runner-1",time.Minute);if err!=nil{t.Fatal(err)}
 if claimed.ClaimVersion!=1||claimed.Status!="claimed"{t.Fatalf("unexpected claim: %+v",claimed)}
 if err=s.CompleteAssignment(ctx,claimed.ID,"wrong-runner",claimed.ClaimVersion);err!=collaboration.ErrLeaseLost{t.Fatalf("expected fenced completion, got %v",err)}
 renewed,err:=s.RenewAssignment(ctx,claimed.ID,"runner-1",claimed.ClaimVersion,time.Minute);if err!=nil{t.Fatal(err)}
 if renewed.ClaimVersion!=claimed.ClaimVersion{t.Fatal("lease renewal must preserve fencing version")}
 if err=s.CompleteAssignment(ctx,claimed.ID,"runner-1",claimed.ClaimVersion);err!=nil{t.Fatal(err)}
}
func TestOutboxDispatchIsIdempotent(t *testing.T){
 ctx:=context.Background();s,db:=testService(t);defer db.Close()
 w,err:=s.CreateWorkspace(ctx,collaboration.Workspace{Name:"Ops",Slug:"ops"});if err!=nil{t.Fatal(err)}
 a,err:=s.CreateAgent(ctx,collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Worker",Role:"worker"});if err!=nil{t.Fatal(err)}
 events,err:=s.PendingOutbox(ctx,100);if err!=nil{t.Fatal(err)}
 d,err:=collaboration.NewDispatcher(db.DB,time.Millisecond);if err!=nil{t.Fatal(err)}
 if err=d.DispatchOnce(ctx,100);err!=nil{t.Fatal(err)}
 if err=d.DispatchOnce(ctx,100);err!=nil{t.Fatal(err)}
 var pending int
 if err=db.DB.QueryRowContext(ctx,"SELECT COUNT(*) FROM collaboration_outbox WHERE published_at IS NULL").Scan(&pending);err!=nil{t.Fatal(err)}
 if pending!=0{t.Fatalf("expected dispatched outbox, got %d pending",pending)}
 var inbox int
 if err=db.DB.QueryRowContext(ctx,"SELECT COUNT(*) FROM collaboration_inbox WHERE agent_id=?",a.ID).Scan(&inbox);err!=nil{t.Fatal(err)}
 if inbox==0{t.Fatal("expected workspace event to be delivered to agent inbox")}
 _=a;_ = events
}

func TestTaskIdempotencyAndHandoffDepth(t *testing.T){
 ctx:=context.Background();s,db:=testService(t);defer db.Close()
 w,err:=s.CreateWorkspace(ctx,collaboration.Workspace{Name:"Build",Slug:"build",MaxDelegationDepth:2,AutonomyMode:"autonomous"});if err!=nil{t.Fatal(err)}
 a,err:=s.CreateAgent(ctx,collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Implementer",Role:"worker"});if err!=nil{t.Fatal(err)}
 original,err:=s.CreateTask(ctx,collaboration.Task{WorkspaceID:w.ID,Title:"Implement feature",IdempotencyKey:"request-1"});if err!=nil{t.Fatal(err)}
 replay,err:=s.CreateTask(ctx,collaboration.Task{WorkspaceID:w.ID,Title:"Different retry title",IdempotencyKey:"request-1"});if err!=nil{t.Fatal(err)}
 if original.ID!=replay.ID{t.Fatal("idempotency key must return the original task")}
 child,assignment,err:=s.HandoffTask(ctx,original.ID,collaboration.Handoff{RequesterAgentID:a.ID,Goal:"Write tests",Recipient:a.ID,IdempotencyKey:"handoff-1"},"planner");if err!=nil{t.Fatal(err)}
 if child.ParentTaskID!=original.ID||assignment.AgentID!=a.ID{t.Fatalf("invalid handoff result: %+v %+v",child,assignment)}
 childAgain,assignmentAgain,err:=s.HandoffTask(ctx,original.ID,collaboration.Handoff{RequesterAgentID:a.ID,Goal:"Retry title",Recipient:a.ID,IdempotencyKey:"handoff-1"},"planner");if err!=nil{t.Fatal(err)}
 if childAgain.ID!=child.ID||assignmentAgain.ID!=assignment.ID{t.Fatal("handoff retry must be idempotent")}
}

func TestInboxReceiptIsIdempotentPerAgent(t *testing.T){
 ctx:=context.Background();s,db:=testService(t);defer db.Close()
 w,err:=s.CreateWorkspace(ctx,collaboration.Workspace{Name:"Inbox",Slug:"inbox"});if err!=nil{t.Fatal(err)}
 a,err:=s.CreateAgent(ctx,collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Inbox Agent",Role:"worker"});if err!=nil{t.Fatal(err)}
 event:=collaboration.Event{ID:"event-1",Type:"test.event",Payload:[]byte("{\"ok\":true}")}
 first,err:=s.ReceiveEvent(ctx,a.ID,event);if err!=nil{t.Fatal(err)}
 second,err:=s.ReceiveEvent(ctx,a.ID,event);if err!=nil{t.Fatal(err)}
 if !first||second{t.Fatalf("expected first delivery only, got %v then %v",first,second)}
 items,err:=s.ListInbox(ctx,a.ID,"pending",20);if err!=nil{t.Fatal(err)}
 if len(items)!=1{t.Fatalf("expected one pending inbox event, got %d",len(items))}
 if err=s.AckInbox(ctx,a.ID,event.ID,"handled");err!=nil{t.Fatal(err)}
}

func TestDecisionGateRequiresApprovalForPrivilegedActions(t *testing.T){
 ctx:=context.Background();s,db:=testService(t);defer db.Close()
 w,err:=s.CreateWorkspace(ctx,collaboration.Workspace{Name:"Governed",Slug:"governed",AutonomyMode:"hybrid"});if err!=nil{t.Fatal(err)}
 a,err:=s.CreateAgent(ctx,collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Delegate",Role:"planner"});if err!=nil{t.Fatal(err)}
 decision,err:=s.EvaluateDecision(ctx,collaboration.DecisionRequest{AgentID:a.ID,Action:"delegate",ResourceType:"task",ResourceID:"task-1",Reason:"split research"});if err!=nil{t.Fatal(err)}
 if decision.Outcome!="approval_required"||decision.ApprovalID==""{t.Fatalf("expected approval gate, got %+v",decision)}
 approvals,err:=s.ListApprovals(ctx,w.ID,"pending",10);if err!=nil{t.Fatal(err)}
 if len(approvals)!=1||approvals[0].ID!=decision.ApprovalID{t.Fatalf("unexpected approvals: %+v",approvals)}
 resolved,err:=s.ResolveApproval(ctx,decision.ApprovalID,"human-reviewer","approved","scope is safe");if err!=nil{t.Fatal(err)}
 if resolved.Status!="approved"{t.Fatalf("expected approved, got %s",resolved.Status)}
 allowed,err:=s.EvaluateDecision(ctx,collaboration.DecisionRequest{AgentID:a.ID,Action:"claim"});if err!=nil{t.Fatal(err)}
 if allowed.Outcome!="allowed"{t.Fatalf("claim should be allowed in hybrid mode: %+v",allowed)}
}

func TestHandoffRequiresHumanApprovalInHybridMode(t *testing.T){
 ctx:=context.Background();s,db:=testService(t);defer db.Close()
 w,err:=s.CreateWorkspace(ctx,collaboration.Workspace{Name:"Hybrid",Slug:"hybrid",AutonomyMode:"hybrid"});if err!=nil{t.Fatal(err)}
 planner,err:=s.CreateAgent(ctx,collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Planner",Role:"planner"});if err!=nil{t.Fatal(err)}
 worker,err:=s.CreateAgent(ctx,collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Worker",Role:"worker"});if err!=nil{t.Fatal(err)}
 parent,err:=s.CreateTask(ctx,collaboration.Task{WorkspaceID:w.ID,Title:"Research",Type:"research"});if err!=nil{t.Fatal(err)}
 handoff:=collaboration.Handoff{RequesterAgentID:planner.ID,Goal:"Collect sources",Recipient:worker.ID,IdempotencyKey:"approval-handoff"}
 _,_,err=s.HandoffTask(ctx,parent.ID,handoff,planner.ID)
 var approvalErr *collaboration.ApprovalRequiredError
 if !errors.As(err,&approvalErr){t.Fatalf("expected approval required, got %v",err)}
 if _,_,err=s.HandoffTask(ctx,parent.ID,handoff,planner.ID);!errors.As(err,&approvalErr){t.Fatalf("expected same pending approval on retry, got %v",err)}
 if _,err=s.ResolveApproval(ctx,approvalErr.ApprovalID,"human","approved","safe delegation");err!=nil{t.Fatal(err)}
 child,assignment,err:=s.HandoffTask(ctx,parent.ID,handoff,planner.ID);if err!=nil{t.Fatal(err)}
 if child.ParentTaskID!=parent.ID||assignment.AgentID!=worker.ID{t.Fatalf("handoff did not complete after approval: %+v %+v",child,assignment)}
}
