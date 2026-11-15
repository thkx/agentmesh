package goal

import (
	"context"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/goal"
)

type Repository interface {
	Create(context.Context, dom.Goal) error
	Update(context.Context, dom.Goal) error
	Get(context.Context, string) (dom.Goal, error)
	List(context.Context, int) ([]dom.Goal, error)
	Cancel(context.Context, string, time.Time) error
}
