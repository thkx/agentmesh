package manager

import (
	"context"
	"errors"
	"testing"

	"github.com/thkx/agentmesh/internal/coding"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
)

type fakeCodingService struct {
	task coding.Task
	getErr error
	starts int
	canceled string
}

func (s *fakeCodingService) Create(_ context.Context, in coding.CreateRequest) (coding.Task, error) {
	s.getErr = nil
	s.task = coding.Task{ID: in.ID, Status: coding.StatusPending}
	return s.task, nil
}
func (s *fakeCodingService) Get(_ context.Context, id string) (coding.Task, error) {
	if s.getErr != nil { return coding.Task{}, s.getErr }
	if s.task.ID != id { return coding.Task{}, coding.ErrNotFound }
	return s.task, nil
}
func (s *fakeCodingService) Start(_ context.Context, id string) (coding.Task, error) {
	s.starts++
	s.task.Status = coding.StatusRunning
	return s.task, nil
}
func (s *fakeCodingService) Cancel(id string) error { s.canceled = id; return nil }

func TestCodingAdapterSubmitIsIdempotentAcrossExistingTask(t *testing.T) {
	service := &fakeCodingService{getErr: coding.ErrNotFound}
	adapter, err := NewCodingAdapter(service)
	if err != nil { t.Fatal(err) }
	input := []byte(`{"id":"coding-1","repository_path":"/tmp/repo","model_id":"model-1","prompt":"implement"}`)
	entity := dom.Entity{ID:"exec-1",TaskID:"coding-1",Input:input}
	for i:=0;i<2;i++ {
		ref, err := adapter.Submit(context.Background(), entity)
		if err != nil { t.Fatal(err) }
		if ref != "coding-1" { t.Fatalf("ref=%q",ref) }
	}
	if service.starts != 1 { t.Fatalf("starts=%d, want 1",service.starts) }
	if err := adapter.Cancel(context.Background(), dom.Entity{RuntimeRef:"coding-1"}); err != nil { t.Fatal(err) }
	if service.canceled != "coding-1" { t.Fatalf("canceled=%q",service.canceled) }
}

type fakeWorkflowEngine struct {
	runs map[string]wf.Run
	getErr error
	starts int
	canceled string
}
func (e *fakeWorkflowEngine) StartQueued(_ context.Context, workflowID, runID string, input map[string]any, _ int) (wf.Run,error) {
	e.starts++
	run:=wf.Run{ID:runID,WorkflowID:workflowID,Status:wf.RunQueued,Context:input}
	e.runs[runID]=run
	return run,nil
}
func (e *fakeWorkflowEngine) GetRun(_ context.Context,id string)(wf.Run,error) {
	if e.getErr!=nil{return wf.Run{},e.getErr}
	run,ok:=e.runs[id]
	if !ok{return wf.Run{},wf.ErrNotFound}
	return run,nil
}
func (e *fakeWorkflowEngine) CancelRun(_ context.Context,id string)error {
	e.canceled=id
	run:=e.runs[id]
	run.Status=wf.RunCanceled
	e.runs[id]=run
	return nil
}

func TestWorkflowAdapterReusesExistingRun(t *testing.T) {
	engine:=&fakeWorkflowEngine{runs:map[string]wf.Run{}}
	adapter,err:=NewWorkflowAdapter(engine,3)
	if err!=nil{t.Fatal(err)}
	entity:=dom.Entity{ID:"exec-wf-1",TaskID:"run-1",Input:[]byte(`{"workflow_id":"wf-1","input":{"goal":"test"}}`)}
	for i:=0;i<2;i++ {
		ref,err:=adapter.Submit(context.Background(),entity)
		if err!=nil{t.Fatal(err)}
		if ref!="run-1"{t.Fatalf("ref=%q",ref)}
	}
	if engine.starts!=1{t.Fatalf("starts=%d, want 1",engine.starts)}
	if err:=adapter.Cancel(context.Background(),dom.Entity{RuntimeRef:"run-1"});err!=nil{t.Fatal(err)}
	if engine.canceled!="run-1"{t.Fatalf("canceled=%q",engine.canceled)}
}

func TestWorkflowAdapterDoesNotMaskStoreFailure(t *testing.T) {
	engine:=&fakeWorkflowEngine{runs:map[string]wf.Run{},getErr:errors.New("database unavailable")}
	adapter,err:=NewWorkflowAdapter(engine,3)
	if err!=nil{t.Fatal(err)}
	entity:=dom.Entity{ID:"exec-wf-2",TaskID:"run-2",Input:[]byte(`{"workflow_id":"wf-1","input":{}}`)}
	if _,err:=adapter.Submit(context.Background(),entity);err==nil{t.Fatal("expected lookup error")}
	if engine.starts!=0{t.Fatalf("unexpected StartQueued calls: %d",engine.starts)}
}

func TestWorkflowAdapterMapsWaitingApprovalToRunning(t *testing.T) {
	engine:=&fakeWorkflowEngine{runs:map[string]wf.Run{"run-3":{ID:"run-3",WorkflowID:"wf-1",Status:wf.RunWaitingApproval}}}
	adapter,err:=NewWorkflowAdapter(engine,3)
	if err!=nil{t.Fatal(err)}
	got,err:=adapter.Observe(context.Background(),dom.Entity{RuntimeRef:"run-3"})
	if err!=nil{t.Fatal(err)}
	if got.Status!=dom.LifecycleRunning{t.Fatalf("status=%s, want running",got.Status)}
}
