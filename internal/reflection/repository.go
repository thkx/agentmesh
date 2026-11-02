package reflection

import (
	"context"
	dom "github.com/thkx/agentmesh/internal/domain/reflection"
)

var ErrNotFound = dom.ErrNotFound

type Repository interface {
	Get(context.Context, string, string) (dom.Reflection, error)
	List(context.Context, string, int) ([]dom.Reflection, error)
	Save(context.Context, dom.Reflection) (dom.Reflection, error)
}
