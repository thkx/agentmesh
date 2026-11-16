package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/thkx/agentmesh/internal/agent/scheduler"
	"github.com/thkx/agentmesh/internal/agentcatalog"
	"github.com/thkx/agentmesh/internal/airesource"
	"github.com/thkx/agentmesh/internal/coding"
	"github.com/thkx/agentmesh/internal/execution/manager"

	"github.com/thkx/agentmesh/internal/agent/executor"
	agentruntime "github.com/thkx/agentmesh/internal/agent/runtime"
	"github.com/thkx/agentmesh/internal/config"
	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
	goalservice "github.com/thkx/agentmesh/internal/goal"
	"github.com/thkx/agentmesh/internal/goalevaluation"
	memoryservice "github.com/thkx/agentmesh/internal/memory"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/platform/logger"
	"go.opentelemetry.io/otel/sdk/trace"
	"github.com/thkx/agentmesh/internal/observability"
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
	LogBuffer             *logger.RingBuffer
	TraceProvider         *trace.TracerProvider
	Metrics               *observability.Metrics
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
	ExecutionManager      *manager.Manager
	CLIExecutionAdapter   *manager.CLIAdapter
	AgentDefinitions      *agentcatalog.Service
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
	CodingService         *coding.Service
	ToolRegistry          *tools.Registry
	ToolEngine            *tools.Engine
	ToolAudit             audit.Store
	AIResources           *airesource.Service
	AIRouter              *airesource.Router
	HTTPHandler           http.Handler
	ShutdownRequests      chan struct{}
}

func (c *Container) Close() error {
	if c == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var shutdownErr error
	// Stop all producers first; then wait for their in-flight work before DB close.
	if c.workerCancel != nil {
		c.workerCancel()
	}
	if c.CLIExecutionAdapter != nil {
		if err := c.CLIExecutionAdapter.CloseContext(shutdownCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown CLI execution adapter: %w", err))
		}
	}
	if c.CodingService != nil {
		if err := c.CodingService.CloseContext(shutdownCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown coding service: %w", err))
		}
	}
	if c.workerDone != nil {
		select {
		case <-c.workerDone:
		case <-shutdownCtx.Done():
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown workers: %w", shutdownCtx.Err()))
		}
	}
	if c.TraceProvider != nil {
		if err := c.TraceProvider.ForceFlush(shutdownCtx); err != nil { shutdownErr = errors.Join(shutdownErr, fmt.Errorf("flush tracing provider: %w", err)) }
		if err := c.TraceProvider.Shutdown(shutdownCtx); err != nil { shutdownErr = errors.Join(shutdownErr, fmt.Errorf("shutdown tracing provider: %w", err)) }
	}
	if shutdownErr != nil {
		return shutdownErr
	}
	if c.DB == nil {
		return nil
	}
	return c.DB.Close()
}
