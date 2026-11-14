package manager

import (
	"context"
	"encoding/json"
	"testing"

	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

type memoryStore struct{ values map[string]dom.Entity }
func newMemoryStore()*memoryStore{return &memoryStore{values:map[string]dom.Entity{}}}
func(s *memoryStore)Create(_ context.Context,e dom.Entity)(dom.Entity,error){s.values[e.ID]=e;return e,nil}
func(s *memoryStore)Get(_ context.Context,id string)(dom.Entity,error){e,ok:=s.values[id];if !ok{return dom.Entity{},dom.ErrNotFound};return e,nil}
func(s *memoryStore)GetByIdempotencyKey(_ context.Context,key string)(dom.Entity,error){for _,e:=range s.values{if e.IdempotencyKey==key{return e,nil}};return dom.Entity{},dom.ErrNotFound}
func(s *memoryStore)List(_ context.Context,_ dom.ListFilter)([]dom.Entity,error){out:=[]dom.Entity{};for _,e:=range s.values{out=append(out,e)};return out,nil}
func(s *memoryStore)BindRuntimeRef(_ context.Context,id,ref string)(dom.Entity,error){e:=s.values[id];e.RuntimeRef=ref;s.values[id]=e;return e,nil}
func(s *memoryStore)Transition(_ context.Context,id string,from []dom.LifecycleStatus,to dom.LifecycleStatus,msg string,result []byte)(dom.Entity,error){e:=s.values[id];allowed:=false;for _,v:=range from{if e.Status==v{allowed=true}};if !allowed{return dom.Entity{},dom.ErrInvalidTransition};e.Status=to;e.Error=msg;e.Result=result;s.values[id]=e;return e,nil}
func(s *memoryStore)CreateAttempt(context.Context,dom.Attempt)(dom.Attempt,error){panic("unused")}
func(s *memoryStore)FinishAttempt(context.Context,string,dom.AttemptStatus,string,[]byte,string)(dom.Attempt,error){panic("unused")}

type fakeAdapter struct{ submitted bool }
func(a *fakeAdapter)Submit(context.Context,dom.Entity)(string,error){a.submitted=true;return "runtime-1",nil}
func(a *fakeAdapter)Observe(context.Context,dom.Entity)(Observation,error){return Observation{Status:dom.LifecycleRunning},nil}
func(a *fakeAdapter)Cancel(context.Context,dom.Entity)error{return nil}

func TestSubmitAndObserve(t *testing.T){
	store:=newMemoryStore();m,err:=New(store);if err!=nil{t.Fatal(err)}
	a:=&fakeAdapter{};if err=m.Register(dom.RuntimeAgent,a);err!=nil{t.Fatal(err)}
	input,_:=json.Marshal(map[string]string{"prompt":"test"})
	e,err:=m.Submit(context.Background(),SubmitRequest{Kind:dom.KindAgent,Runtime:dom.RuntimeAgent,TaskID:"task-1",Input:input,IdempotencyKey:"key-1"})
	if err!=nil{t.Fatal(err)};if !a.submitted || e.RuntimeRef!="runtime-1"{t.Fatalf("adapter not submitted or ref missing: %+v",e)}
	got,err:=m.Get(context.Background(),e.ID);if err!=nil{t.Fatal(err)};if got.Status!=dom.LifecycleRunning{t.Fatalf("status=%s",got.Status)}
}
