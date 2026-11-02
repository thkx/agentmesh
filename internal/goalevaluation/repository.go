package goalevaluation

import (
	"context"

	evaldom "github.com/thkx/agentmesh/internal/domain/goalevaluation"
)

var (
	ErrNotFound          = evaldom.ErrNotFound
	ErrExecutionNotFound = evaldom.ErrExecutionNotFound
	ErrGoalNotActive     = evaldom.ErrGoalNotActive
)

type ExecutionLink = evaldom.ExecutionLink

type Repository interface {
	FindExecution(context.Context, string, string) (ExecutionLink, error)
	Get(context.Context, string, string) (evaldom.Evaluation, error)
	List(context.Context, string, int) ([]evaldom.Evaluation, error)
	Save(context.Context, evaldom.Evaluation, bool) (evaldom.Evaluation, error)
}
