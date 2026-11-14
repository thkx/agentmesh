package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	dom "github.com/thkx/agentmesh/internal/domain/execution"
	"github.com/thkx/agentmesh/internal/execution/repository"
)

var (
	ErrAdapterNotRegistered = errors.New("execution runtime adapter not registered")
	ErrInvalidRequest = errors.New("invalid execution submission")
)

type Observation struct {
	Status dom.LifecycleStatus
	Result json.RawMessage
	Error string
}

type Adapter interface {
	Submit(context.Context, dom.Entity) (string, error)
	Observe(context.Context, dom.Entity) (Observation, error)
	Cancel(context.Context, dom.Entity) error
}

type SubmitRequest struct {
	Kind dom.Kind
	Runtime dom.Runtime
	TaskID string
	Input json.RawMessage
	IdempotencyKey string
}

type Manager struct {
	store repository.Store
	adapters map[dom.Runtime]Adapter
}

func New(store repository.Store) (*Manager,error) {
	if store==nil{return nil,errors.New("execution store is nil")}
	return &Manager{store:store,adapters:make(map[dom.Runtime]Adapter)},nil
}

func (m *Manager) Register(runtime dom.Runtime,adapter Adapter) error {
	if strings.TrimSpace(string(runtime))=="" || adapter==nil{return ErrInvalidRequest}
	if _,exists:=m.adapters[runtime];exists{return fmt.Errorf("adapter already registered for runtime %q",runtime)}
	m.adapters[runtime]=adapter
	return nil
}

func (m *Manager) Submit(ctx context.Context,in SubmitRequest)(dom.Entity,error) {
	if !validKindRuntime(in.Kind,in.Runtime) || len(in.Input)==0 || !json.Valid(in.Input) || len(in.Input)>1<<20 || len(in.TaskID)>128 || len(in.IdempotencyKey)>256 {
		return dom.Entity{},ErrInvalidRequest
	}
	adapter,ok:=m.adapters[in.Runtime];if !ok{return dom.Entity{},ErrAdapterNotRegistered}
	if in.IdempotencyKey!="" {
		old,err:=m.store.GetByIdempotencyKey(ctx,in.IdempotencyKey)
		if err==nil {
			if old.Kind!=in.Kind || old.Runtime!=in.Runtime || old.TaskID!=in.TaskID || string(old.Input)!=string(in.Input) {return dom.Entity{},dom.ErrConflict}
			return m.Get(ctx,old.ID)
		}
		if !errors.Is(err,dom.ErrNotFound){return dom.Entity{},err}
	}
	now:=time.Now().UTC()
	e:=dom.Entity{ID:uuid.NewString(),Kind:in.Kind,Runtime:in.Runtime,TaskID:in.TaskID,Status:dom.LifecycleQueued,Input:append(json.RawMessage(nil),in.Input...),IdempotencyKey:in.IdempotencyKey,CreatedAt:now,UpdatedAt:now}
	created,err:=m.store.Create(ctx,e);if err!=nil{return dom.Entity{},err}
	if created.ID!=e.ID {return m.Get(ctx,created.ID)}
	ref,err:=adapter.Submit(ctx,created)
	if err!=nil {
		failed,transitionErr:=m.store.Transition(context.Background(),created.ID,[]dom.LifecycleStatus{dom.LifecycleQueued},dom.LifecycleFailed,err.Error(),nil)
		return failed,errors.Join(err,transitionErr)
	}
	if ref!="" {
		created,err=m.store.BindRuntimeRef(ctx,created.ID,ref)
		if err!=nil{return created,fmt.Errorf("bind runtime reference; execution may already be queued: %w",err)}
	}
	return created,nil
}

func (m *Manager) Get(ctx context.Context,id string)(dom.Entity,error) {
	e,err:=m.store.Get(ctx,id);if err!=nil{return dom.Entity{},err}
	adapter,ok:=m.adapters[e.Runtime];if !ok{return e,nil}
	observed,err:=adapter.Observe(ctx,e);if err!=nil{return e,err}
	if observed.Status=="" || observed.Status==e.Status{return e,nil}
	if !dom.CanTransition(e.Status,observed.Status){return e,dom.ErrInvalidTransition}
	return m.store.Transition(ctx,e.ID,[]dom.LifecycleStatus{e.Status},observed.Status,observed.Error,observed.Result)
}

func (m *Manager) Cancel(ctx context.Context,id string) error {
	e,err:=m.store.Get(ctx,id);if err!=nil{return err}
	if e.Status!=dom.LifecycleQueued && e.Status!=dom.LifecycleRunning{return dom.ErrInvalidTransition}
	adapter,ok:=m.adapters[e.Runtime];if !ok{return ErrAdapterNotRegistered}
	return adapter.Cancel(ctx,e)
}

func (m *Manager) List(ctx context.Context,f dom.ListFilter)([]dom.Entity,error) {
	return m.store.List(ctx,f)
}

func validKindRuntime(kind dom.Kind, runtime dom.Runtime) bool {
 switch kind { case dom.KindAgent: return runtime==dom.RuntimeAgent; case dom.KindCoding: return runtime==dom.RuntimeCoding; case dom.KindWorkflow: return runtime==dom.RuntimeWorkflow; case dom.KindCLI: return runtime==dom.RuntimeCLI; default: return false }
}
