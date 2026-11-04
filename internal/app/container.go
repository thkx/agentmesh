package app

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/thkx/agentmesh/internal/agent/scheduler"

	"github.com/thkx/agentmesh/internal/agent/executor"
	agentruntime "github.com/thkx/agentmesh/internal/agent/runtime"
	"github.com/thkx/agentmesh/internal/config"
	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
	goalservice "github.com/thkx/agentmesh/internal/goal"
	"github.com/thkx/agentmesh/internal/goalevaluation"
	memoryservice "github.com/thkx/agentmesh/internal/memory"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/planning"
	planworkflow "github.com/thkx/agentmesh/internal/planworkflow"
	"github.com/thkx/agentmesh/internal/reflection"
	"github.com/thkx/agentmesh/internal/tools"
	"github.com/thkx/agentmesh/internal/tools/audit"
	wfruntime "github.com/thkx/agentmesh/internal/workflow/runtime"
	"github.com/thkx/agentmesh/internal/workspace"
)

type Container struct {
	Config                config.Config
	Logger                *slog.Logger
	DB                    *sql.DB
	RuntimeRepository     provider.CatalogRepository
	CredentialStore       appcredential.Store
	CredentialResolver    appcredential.Resolver
	CredentialManager     appcredential.Manager
	Providers             *provider.ProviderRegistry
	Models                *provider.ModelRegistry
	Adapters              *modelgateway.Registry
	Gateway               *modelgateway.Gateway
	ProviderRuntime       *modelgateway.ProviderRuntime
	AgentExecutor         executor.AgentExecutor
	AgentRuntime          *agentruntime.Runtime
	WorkflowEngine        *wfruntime.Engine
	GoalService           *goalservice.Service
	PlanningService       *planning.Service
	PlanExecutionService  *planworkflow.Service
	GoalEvaluationService *goalevaluation.Service
	ReflectionService     *reflection.Service
	MemoryService         *memoryservice.Service
	WorkflowWorkers       []*wfruntime.Worker
	SchedulerStore        *scheduler.SQLiteStore
	SchedulerWorker       *scheduler.Worker
	workerCancel          context.CancelFunc
	workerDone            chan struct{}
	Workspaces            *workspace.Manager
	ToolRegistry          *tools.Registry
	ToolEngine            *tools.Engine
	ToolAudit             *audit.Memory
	HTTPHandler           http.Handler
}

func (c *Container) Close() error {
	if c == nil {
		return nil
	}
	if c.workerCancel != nil {
		c.workerCancel()
	}
	if c.workerDone != nil {
		<-c.workerDone
	}
	if c.DB == nil {
		return nil
	}
	return c.DB.Close()
}
