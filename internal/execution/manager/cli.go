package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/thkx/agentmesh/internal/agent/executor"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
	"github.com/thkx/agentmesh/internal/execution/repository"
)

type CLIAdapter struct{
	executor executor.AgentExecutor
	store repository.Store
	mu sync.RWMutex
	cancels map[string]context.CancelFunc
	results map[string]Observation
}

func NewCLIAdapter(executor executor.AgentExecutor,store repository.Store)(*CLIAdapter,error){
	if executor==nil||store==nil{return nil,errors.New("CLI execution adapter dependencies are nil")}
	return &CLIAdapter{executor:executor,store:store,cancels:make(map[string]context.CancelFunc),results:make(map[string]Observation)},nil
}

func(a *CLIAdapter)Submit(ctx context.Context,e dom.Entity)(string,error){
	var request dom.Request
	if err:=json.Unmarshal(e.Input,&request);err!=nil{return "",fmt.Errorf("decode CLI execution input: %w",err)}
	if request.ID==""{request.ID=e.TaskID};if request.ID==""{request.ID=e.ID}
	if err:=request.Validate();err!=nil{return "",err}
	now:=time.Now().UTC()
	attemptID:=e.ID+"-a-1"
	if _,err:=a.store.Transition(ctx,e.ID,[]dom.LifecycleStatus{dom.LifecycleQueued},dom.LifecycleRunning,"",nil);err!=nil{return "",err}
	attempt:=dom.Attempt{ID:attemptID,ExecutionID:e.ID,Number:1,Runtime:dom.RuntimeCLI,RuntimeRef:request.ID,Status:dom.AttemptRunning,Input:e.Input,StartedAt:now}
	if _,err:=a.store.CreateAttempt(ctx,attempt);err!=nil{return "",err}
	runCtx,cancel:=context.WithCancel(context.Background())
	if request.Timeout>0{runCtx,cancel=context.WithTimeout(context.Background(),request.Timeout)}
	a.mu.Lock();a.cancels[request.ID]=cancel;a.results[request.ID]=Observation{Status:dom.LifecycleRunning,Attempt:1};a.mu.Unlock()
	go func(){
		result,runErr:=a.executor.Execute(runCtx,request)
		cancel()
		var raw []byte
		target:=dom.LifecycleFailed
		attemptStatus:=dom.AttemptFailed
		message:=""
		if result!=nil{
			raw,_=json.Marshal(result)
			switch result.Status{
			case dom.StatusSucceeded:target=dom.LifecycleSucceeded;attemptStatus=dom.AttemptSucceeded
			case dom.StatusCanceled:target=dom.LifecycleCanceled;attemptStatus=dom.AttemptCanceled
			case dom.StatusTimedOut:target=dom.LifecycleTimedOut;attemptStatus=dom.AttemptTimedOut
			default:target=dom.LifecycleFailed;attemptStatus=dom.AttemptFailed
			}
		}
		if runErr!=nil{message=runErr.Error();if errors.Is(runErr,context.Canceled){target=dom.LifecycleCanceled;attemptStatus=dom.AttemptCanceled};if errors.Is(runErr,context.DeadlineExceeded){target=dom.LifecycleTimedOut;attemptStatus=dom.AttemptTimedOut}}
		_,_=a.store.FinishAttempt(context.Background(),attemptID,attemptStatus,request.ID,raw,message)
		_,_=a.store.Transition(context.Background(),e.ID,[]dom.LifecycleStatus{dom.LifecycleRunning},target,message,raw)
		a.mu.Lock();delete(a.cancels,request.ID);a.results[request.ID]=Observation{Status:target,Attempt:1,Result:raw,Error:message};a.mu.Unlock()
	}()
	return request.ID,nil
}

func(a *CLIAdapter)Observe(_ context.Context,e dom.Entity)(Observation,error){
	id:=e.RuntimeRef;if id==""{id=e.TaskID}
	a.mu.RLock();v,ok:=a.results[id];a.mu.RUnlock()
	if ok{return v,nil}
	if e.Status==dom.LifecycleRunning{return Observation{Status:dom.LifecycleInterrupted,Error:"CLI process state was lost; automatic replay is disabled"},nil}
	return Observation{Status:e.Status,Result:e.Result,Error:e.Error},nil
}

func(a *CLIAdapter)Cancel(ctx context.Context,e dom.Entity)error{
	id:=e.RuntimeRef;if id==""{id=e.TaskID}
	a.mu.RLock();cancel,ok:=a.cancels[id];a.mu.RUnlock()
	if ok{cancel();return nil}
	return a.executor.Cancel(ctx,id)
}
