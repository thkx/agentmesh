package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/thkx/agentmesh/internal/agent/scheduler"
	agentstate "github.com/thkx/agentmesh/internal/agent/state"
	agentdom "github.com/thkx/agentmesh/internal/domain/agent"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

type AgentScheduler interface {
	Enqueue(context.Context, agentdom.Task, string, int) (scheduler.Job,error)
	Get(context.Context,string)(scheduler.Job,error)
	CancelTask(context.Context,string) error
}

type AgentRuntime interface {
	Cancel(string) error
	GetSnapshot(context.Context,string)(agentstate.Snapshot,error)
}

type AgentAdapter struct {
	scheduler AgentScheduler
	runtime AgentRuntime
	maxAttempts int
}

func NewAgentAdapter(queue AgentScheduler, runtime AgentRuntime, maxAttempts int)(*AgentAdapter,error) {
	if queue==nil || runtime==nil{return nil,errors.New("agent execution adapter dependencies are nil")}
	if maxAttempts<1{maxAttempts=3}
	return &AgentAdapter{scheduler:queue,runtime:runtime,maxAttempts:maxAttempts},nil
}

func (a *AgentAdapter) Submit(ctx context.Context,e dom.Entity)(string,error) {
	var task agentdom.Task
	if err:=json.Unmarshal(e.Input,&task);err!=nil{return "",fmt.Errorf("decode agent execution input: %w",err)}
	if err:=task.Validate();err!=nil{return "",err}
	if e.TaskID!="" && e.TaskID!=task.ID{return "",dom.ErrInvalidEntity}
	job,err:=a.scheduler.Enqueue(ctx,task,task.ID,a.maxAttempts)
	if err!=nil{return "",err}
	return job.ID,nil
}

func (a *AgentAdapter) Observe(ctx context.Context,e dom.Entity)(Observation,error) {
	ref:=e.RuntimeRef
	if ref==""{ref=e.TaskID}
	job,err:=a.scheduler.Get(ctx,ref)
	if err!=nil{return Observation{},err}
	switch job.Status {
	case scheduler.Queued:
		return Observation{Status:dom.LifecycleQueued,Error:job.LastError},nil
	case scheduler.Running:
		return Observation{Status:dom.LifecycleRunning},nil
	case scheduler.Succeeded:
		snapshot,loadErr:=a.runtime.GetSnapshot(ctx,job.Task.ID)
		if loadErr!=nil{return Observation{Status:dom.LifecycleNeedsReconciliation,Error:"scheduler reports success but agent state is missing"},nil}
		result,marshalErr:=json.Marshal(snapshot.Result)
		if marshalErr!=nil{return Observation{},marshalErr}
		return Observation{Status:dom.LifecycleSucceeded,Result:result},nil
	case scheduler.Failed:
		return Observation{Status:dom.LifecycleFailed,Error:job.LastError},nil
	case scheduler.Canceled:
		return Observation{Status:dom.LifecycleCanceled,Error:job.LastError},nil
	case scheduler.Interrupted:
		return Observation{Status:dom.LifecycleInterrupted,Error:job.LastError},nil
	default:
		return Observation{},fmt.Errorf("unknown agent scheduler status %q",job.Status)
	}
}

func (a *AgentAdapter) Cancel(ctx context.Context,e dom.Entity)error {
	ref:=e.RuntimeRef
	if ref==""{ref=e.TaskID}
	job,err:=a.scheduler.Get(ctx,ref)
	if err!=nil{return err}
	switch job.Status {
	case scheduler.Queued:
		return a.scheduler.CancelTask(ctx,job.Task.ID)
	case scheduler.Running:
		return a.runtime.Cancel(job.Task.ID)
	case scheduler.Succeeded,scheduler.Failed,scheduler.Canceled,scheduler.Interrupted:
		return dom.ErrInvalidTransition
	default:
		return errors.New("unsupported agent scheduler state")
	}
}
