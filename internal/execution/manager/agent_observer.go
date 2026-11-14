package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/thkx/agentmesh/internal/agent/scheduler"
	agentdom "github.com/thkx/agentmesh/internal/domain/agent"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
	"github.com/thkx/agentmesh/internal/execution/repository"
)

type AgentObserver struct{ store repository.Store }

func NewAgentObserver(store repository.Store)(*AgentObserver,error){
	if store==nil{return nil,errors.New("agent execution observer store is nil")}
	return &AgentObserver{store:store},nil
}

func(o *AgentObserver)find(ctx context.Context,taskID string)(dom.Entity,error){
	items,err:=o.store.List(ctx,dom.ListFilter{Runtime:dom.RuntimeAgent,TaskID:taskID,Limit:5})
	if err!=nil{return dom.Entity{},err}
	if len(items)==0{return dom.Entity{},dom.ErrNotFound}
	if len(items)>1{return dom.Entity{},fmt.Errorf("multiple unified executions reference agent task %q",taskID)}
	return items[0],nil
}

func(o *AgentObserver)Started(ctx context.Context,job scheduler.Job)error{
	e,err:=o.find(ctx,job.Task.ID)
	if errors.Is(err,dom.ErrNotFound){return nil}
	if err!=nil{return err}
	input,_:=json.Marshal(job.Task)
	now:=time.Now().UTC()
	attempt:=dom.Attempt{ID:e.ID+"-a-"+strconv.Itoa(job.Attempts),ExecutionID:e.ID,Number:job.Attempts,Runtime:dom.RuntimeAgent,RuntimeRef:job.ID,Status:dom.AttemptRunning,Input:input,StartedAt:now}
	if _,err=o.store.CreateAttempt(ctx,attempt);err!=nil{return err}
	_,err=o.store.Transition(ctx,e.ID,[]dom.LifecycleStatus{dom.LifecycleQueued,dom.LifecycleRunning},dom.LifecycleRunning,"",nil)
	return err
}

func(o *AgentObserver)Finished(ctx context.Context,job scheduler.Job,result agentdom.Result,runErr error)error{
	e,err:=o.find(ctx,job.Task.ID)
	if errors.Is(err,dom.ErrNotFound){return nil}
	if err!=nil{return err}
	resultJSON,marshalErr:=json.Marshal(result)
	if marshalErr!=nil{return marshalErr}
	attemptID:=e.ID+"-a-"+strconv.Itoa(job.Attempts)
	attemptStatus:=dom.AttemptSucceeded
	message:=""
	if runErr!=nil{
		message=runErr.Error()
		switch{
		case errors.Is(runErr,context.Canceled):attemptStatus=dom.AttemptCanceled
		case errors.Is(runErr,context.DeadlineExceeded):attemptStatus=dom.AttemptTimedOut
		default:attemptStatus=dom.AttemptFailed
		}
	}
	if _,finishErr:=o.store.FinishAttempt(ctx,attemptID,attemptStatus,job.ID,resultJSON,message);finishErr!=nil && !errors.Is(finishErr,dom.ErrInvalidTransition){return finishErr}
	latest,err:=o.store.Get(ctx,e.ID);if err!=nil{return err}
	var target dom.LifecycleStatus
	switch job.Status{
	case scheduler.Succeeded:target=dom.LifecycleSucceeded
	case scheduler.Queued:target=dom.LifecycleQueued
	case scheduler.Canceled:target=dom.LifecycleCanceled
	case scheduler.Interrupted:target=dom.LifecycleInterrupted
	case scheduler.Failed:target=dom.LifecycleFailed
	default:
		fresh,loadErr:=o.find(ctx,job.Task.ID);if loadErr==nil{latest=fresh}
		// The queue may have transitioned to retryable queued after Fail.
		target=dom.LifecycleFailed
	}
	if latest.Status==target{return nil}
	if !dom.CanTransition(latest.Status,target){return dom.ErrInvalidTransition}
	_,err=o.store.Transition(ctx,latest.ID,[]dom.LifecycleStatus{latest.Status},target,message,resultJSON)
	return err
}

var _ scheduler.Observer = (*AgentObserver)(nil)
var _ = uuid.NewString
