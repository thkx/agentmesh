package executor

import (
	"context"
	"github.com/thkx/agentmesh/internal/domain/execution"
)

type AgentExecutor interface {
	Execute(ctx context.Context, request execution.Request) (*execution.Result, error)
	Cancel(ctx context.Context, executionID string) error
}
