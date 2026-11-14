package collaboration_test

import (
 "context"
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
