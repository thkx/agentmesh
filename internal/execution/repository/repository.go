package repository

import (
	"context"

	dom "github.com/thkx/agentmesh/internal/domain/execution"
)

type Store interface {
	Create(context.Context, dom.Entity) (dom.Entity, error)
	Get(context.Context, string) (dom.Entity, error)
	GetByIdempotencyKey(context.Context, string) (dom.Entity, error)
	List(context.Context, dom.ListFilter) ([]dom.Entity, error)
	Transition(context.Context, string, []dom.LifecycleStatus, dom.LifecycleStatus, string, []byte) (dom.Entity, error)
	CreateAttempt(context.Context, dom.Attempt) (dom.Attempt, error)
	FinishAttempt(context.Context, string, dom.AttemptStatus, string, []byte, string) (dom.Attempt, error)
}
