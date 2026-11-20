package manager

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thkx/agentmesh/internal/coding"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

type fakeCodingAdapterService struct {
	task coding.Task
	getErr error
	startCalls int
	boundExecution string
	boundAttempt string
}

func (f *fakeCodingAdapterService) Create(_ context.Context, in coding.CreateRequest) (coding.Task, error) {
	f.task = coding.Task{ID:in.ID,Status:coding.StatusPending}
	return f.task,nil
}
func (f *fakeCodingAdapterService) Get(_ context.Context, _ string) (coding.Task,error) {
	if f.getErr != nil { return coding.Task{},f.getErr }
	return f.task,nil
}
func (f *fakeCodingAdapterService) Start(_ context.Context,_ string) (coding.Task,error) {
	f.startCalls++
	return f.task,nil
}
func (f *fakeCodingAdapterService) Cancel(string) error { return nil }
func (f *fakeCodingAdapterService) BindExecution(_ context.Context,_ string,executionID,attemptID string) error {
	f.boundExecution=executionID
	f.boundAttempt=attemptID
	f.task.ExecutionID=executionID
	f.task.AttemptID=attemptID
	return nil
}

func TestCodingAdapterSubmitBindsCanonicalAttemptAndIsIdempotent(t *testing.T) {
	service:=&fakeCodingAdapterService{getErr:coding.ErrNotFound}
	adapter,err:=NewCodingAdapter(service)
	if err!=nil { t.Fatal(err) }
	input,_:=json.Marshal(coding.CreateRequest{ID:"coding-task-1",RepositoryPath:"/tmp/repo",ModelID:"model",Prompt:"change"})
	entity:=dom.Entity{ID:"execution-1",Kind:dom.KindCoding,Runtime:dom.RuntimeCoding,TaskID:"coding-task-1",Input:input}
	ref,err:=adapter.Submit(context.Background(),entity)
	if err!=nil { t.Fatal(err) }
	if ref!="coding-task-1" || service.startCalls!=1 { t.Fatalf("ref=%q starts=%d",ref,service.startCalls) }
	if service.boundExecution!="execution-1" || service.boundAttempt!="execution-1-a-1" { t.Fatalf("provenance not bound: execution=%q attempt=%q",service.boundExecution,service.boundAttempt) }
	service.task.Status=coding.StatusRunning
	if _,err=adapter.Submit(context.Background(),entity);err!=nil { t.Fatalf("repeated submit should be idempotent: %v",err) }
	if service.startCalls!=1 { t.Fatalf("repeated submit started task again: %d",service.startCalls) }
}

func TestCodingAdapterMapsInterruptedOutcome(t *testing.T) {
	service:=&fakeCodingAdapterService{task:coding.Task{ID:"coding-task-2",Status:coding.StatusInterrupted,OutcomeUnknown:true,Error:"process restarted"}}
	adapter,err:=NewCodingAdapter(service)
	if err!=nil { t.Fatal(err) }
	got,err:=adapter.Observe(context.Background(),dom.Entity{ID:"execution-2",RuntimeRef:"coding-task-2"})
	if err!=nil { t.Fatal(err) }
	if got.Status!=dom.LifecycleInterrupted || got.AttemptOutcome!="" { t.Fatalf("unexpected observation: %+v",got) }
}
