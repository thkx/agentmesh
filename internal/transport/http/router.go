package http

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/agent/executor"
	agentruntime "github.com/thkx/agentmesh/internal/agent/runtime"
	"github.com/thkx/agentmesh/internal/agent/scheduler"
	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/tools"
	"github.com/thkx/agentmesh/internal/tools/audit"
	"github.com/thkx/agentmesh/internal/transport/http/handlers"
	wfruntime "github.com/thkx/agentmesh/internal/workflow/runtime"
	"github.com/thkx/agentmesh/internal/workspace"
)

type Dependencies struct {
	DB                   *sql.DB
	Providers            *provider.ProviderRegistry
	Models               *provider.ModelRegistry
	Adapters             *modelgateway.Registry
	ProviderRuntime      *modelgateway.ProviderRuntime
	Repository           provider.CatalogRepository
	Gateway              *modelgateway.Gateway
	CredentialManager    appcredential.Manager
	AgentExecutor        executor.AgentExecutor
	AgentRuntime         *agentruntime.Runtime
	WorkflowEngine       *wfruntime.Engine
	SchedulerStore       scheduler.Store
	SchedulerMaxAttempts int
	WorkflowMaxAttempts  int
	Workspaces           *workspace.Manager
	ToolRegistry         *tools.Registry
	ToolEngine           *tools.Engine
	ToolAudit            *audit.Memory
}

func NewRouter(deps Dependencies) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
		c.Next()
	})
	router.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	router.GET("/readyz", func(c *gin.Context) {
		if deps.DB == nil || deps.Providers == nil || deps.Models == nil || deps.Adapters == nil || deps.ProviderRuntime == nil || deps.Gateway == nil || deps.Repository == nil || deps.AgentExecutor == nil || deps.AgentRuntime == nil || deps.Workspaces == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := deps.DB.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "dependency": "database"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
	api := router.Group("/api/v1")
	h := handlers.NewCatalogHandler(deps.Providers, deps.Models, deps.Adapters, deps.ProviderRuntime, deps.Repository, deps.Gateway)
	h.RegisterRoutes(api)
	if deps.CredentialManager != nil {
		handlers.NewCredentialHandler(deps.CredentialManager).RegisterRoutes(api)
	}
	if deps.AgentExecutor != nil {
		handlers.NewExecutionHandler(deps.AgentExecutor).RegisterRoutes(api)
	}
	if deps.Workspaces != nil {
		handlers.NewWorkspaceHandler(deps.Workspaces).RegisterRoutes(api)
	}
	if deps.AgentRuntime != nil {
		agentHandler := handlers.NewAgentHandler(deps.AgentRuntime, deps.SchedulerStore)
		agentHandler.SetMaxAttempts(deps.SchedulerMaxAttempts)
		agentHandler.RegisterRoutes(api)
	}
	if deps.WorkflowEngine != nil {
		workflowHandler := handlers.NewWorkflowHandler(deps.WorkflowEngine)
		workflowHandler.SetMaxAttempts(deps.WorkflowMaxAttempts)
		workflowHandler.RegisterRoutes(api)
	}
	if deps.ToolRegistry != nil && deps.ToolEngine != nil && deps.Workspaces != nil && deps.ToolAudit != nil {
		handlers.NewToolHandler(deps.ToolEngine, deps.ToolRegistry, deps.Workspaces, deps.ToolAudit).RegisterRoutes(api)
	}
	return router
}
