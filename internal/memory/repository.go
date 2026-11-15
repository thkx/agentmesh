package memory

import (
	"context"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/memory"
)

type Repository interface {
	IngestCandidates(context.Context, []dom.Memory) ([]dom.Memory, error)
	Get(context.Context, string) (dom.Memory, error)
	ListByGoal(context.Context, string, int) ([]dom.Memory, error)
	Promote(context.Context, string, dom.Type, dom.ScopeType, string, string, time.Time) (dom.Memory, error)
	Archive(context.Context, string, time.Time) (dom.Memory, error)
	Reject(context.Context, string, string, time.Time) (dom.Memory, error)
	SearchActive(context.Context, string, []string, int) ([]dom.Memory, error)
	Touch(context.Context, []string, time.Time) error
}
