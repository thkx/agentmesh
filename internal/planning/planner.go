package planning

import (
	"context"

	goaldom "github.com/thkx/agentmesh/internal/domain/goal"
	pland "github.com/thkx/agentmesh/internal/domain/planning"
)

type Draft struct {
	Objective string       `json:"objective"`
	Steps     []pland.Step `json:"steps"`
}

type Planner interface {
	Generate(context.Context, goaldom.Goal, string) (Draft, error)
}

type Repository interface {
	Create(context.Context, pland.Plan) (pland.Plan, error)
	Get(context.Context, string, int) (pland.Plan, error)
	List(context.Context, string, int) ([]pland.Plan, error)
}
