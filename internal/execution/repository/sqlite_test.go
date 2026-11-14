package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

func TestSQLiteStoreExecutionAndAttempts(t *testing.T) {
	db,err:=sql.Open("sqlite",":memory:")
	if err!=nil{t.Fatal(err)}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema:=[]string{
		`CREATE TABLE executions(id TEXT PRIMARY KEY,kind TEXT NOT NULL,runtime TEXT NOT NULL,runtime_ref TEXT,task_id TEXT,status TEXT NOT NULL,input_json TEXT,result_json TEXT,error_text TEXT NOT NULL DEFAULT '',idempotency_key TEXT,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,started_at INTEGER,finished_at INTEGER)`,
		`CREATE TABLE execution_attempts(id TEXT PRIMARY KEY,execution_id TEXT NOT NULL REFERENCES executions(id),attempt_number INTEGER NOT NULL,runtime TEXT NOT NULL,runtime_ref TEXT,status TEXT NOT NULL,input_json TEXT,result_json TEXT,error_text TEXT NOT NULL DEFAULT '',started_at INTEGER NOT NULL,finished_at INTEGER,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,UNIQUE(execution_id,attempt_number))`,
	}
	for _,statement:=range schema{if _,err=db.Exec(statement);err!=nil{t.Fatal(err)}}
	store,err:=NewSQLiteStore(db);if err!=nil{t.Fatal(err)}
	ctx:=context.Background()
	input:=json.RawMessage(`{"id":"task-1","prompt":"hello"}`)
	now:=time.Now().UTC()
	created,err:=store.Create(ctx,dom.Entity{ID:"exec-1",Kind:dom.KindAgent,Runtime:dom.RuntimeAgent,TaskID:"task-1",Status:dom.LifecycleQueued,Input:input,CreatedAt:now,UpdatedAt:now})
	if err!=nil{t.Fatalf("create execution: %v",err)}
	if _,err=store.BindRuntimeRef(ctx,created.ID,"task-1");err!=nil{t.Fatalf("bind runtime ref: %v",err)}
	if _,err=store.Transition(ctx,created.ID,[]dom.LifecycleStatus{dom.LifecycleQueued},dom.LifecycleRunning,"",nil);err!=nil{t.Fatalf("start execution: %v",err)}
	attempt:=dom.Attempt{ID:"exec-1-a-1",ExecutionID:created.ID,Number:1,Runtime:dom.RuntimeAgent,RuntimeRef:"task-1",Status:dom.AttemptRunning,Input:input,StartedAt:now}
	if _,err=store.CreateAttempt(ctx,attempt);err!=nil{t.Fatalf("create attempt: %v",err)}
	// A second insert for the same execution/attempt number is idempotent.
	attempt.ID="different-id"
	if got,err:=store.CreateAttempt(ctx,attempt);err!=nil||got.ID!="exec-1-a-1"{t.Fatalf("idempotent attempt create: got=%+v err=%v",got,err)}
	attempts,err:=store.ListAttempts(ctx,created.ID);if err!=nil{t.Fatalf("list attempts with one SQLite connection: %v",err)}
	if len(attempts)!=1{t.Fatalf("attempt count=%d",len(attempts))}
	result:=json.RawMessage(`{"content":"done"}`)
	if _,err=store.FinishAttempt(ctx,"exec-1-a-1",dom.AttemptSucceeded,"task-1",result,"");err!=nil{t.Fatalf("finish attempt: %v",err)}
	if _,err=store.Transition(ctx,created.ID,[]dom.LifecycleStatus{dom.LifecycleRunning},dom.LifecycleSucceeded,"",result);err!=nil{t.Fatalf("finish execution: %v",err)}
	got,err:=store.Get(ctx,created.ID);if err!=nil{t.Fatal(err)}
	if got.Status!=dom.LifecycleSucceeded||got.StartedAt==nil||got.FinishedAt==nil{t.Fatalf("unexpected final execution: %+v",got)}
}
