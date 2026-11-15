package http

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/agent/contextmanager"
	"github.com/thkx/agentmesh/internal/agent/executor"
	agentruntime "github.com/thkx/agentmesh/internal/agent/runtime"
	"github.com/thkx/agentmesh/internal/agent/scheduler"
	"github.com/thkx/agentmesh/internal/agentcatalog"
	"github.com/thkx/agentmesh/internal/airesource"
	"github.com/thkx/agentmesh/internal/coding"
	"github.com/thkx/agentmesh/internal/collaboration"
	"github.com/thkx/agentmesh/internal/conversation"
	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/execution/manager"
	goalservice "github.com/thkx/agentmesh/internal/goal"
	"github.com/thkx/agentmesh/internal/goalevaluation"
	"github.com/thkx/agentmesh/internal/memory"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/planning"
	"github.com/thkx/agentmesh/internal/planworkflow"
	"github.com/thkx/agentmesh/internal/reflection"
	"github.com/thkx/agentmesh/internal/tools"
	"github.com/thkx/agentmesh/internal/tools/audit"
	"github.com/thkx/agentmesh/internal/transport/http/dashboard"
	"github.com/thkx/agentmesh/internal/transport/http/handlers"
	wfruntime "github.com/thkx/agentmesh/internal/workflow/runtime"
	"github.com/thkx/agentmesh/internal/workspace"
)

type Dependencies struct {
	DB                    *sql.DB
	Providers             *provider.ProviderRegistry
	Models                *provider.ModelRegistry
	Adapters              *modelgateway.Registry
	ProviderRuntime       *modelgateway.ProviderRuntime
	Repository            provider.CatalogRepository
	Gateway               *modelgateway.Gateway
	CredentialManager     appcredential.Manager
	AgentExecutor         executor.AgentExecutor
	AgentRuntime          *agentruntime.Runtime
	ExecutionManager      *manager.Manager
	WorkflowEngine        *wfruntime.Engine
	GoalService           *goalservice.Service
	PlanningService       *planning.Service
	PlanExecutionService  *planworkflow.Service
	GoalEvaluationService *goalevaluation.Service
	ReflectionService     *reflection.Service
	MemoryService         *memory.Service
	SchedulerStore        scheduler.Store
	SchedulerMaxAttempts  int
	WorkflowMaxAttempts   int
	Workspaces            *workspace.Manager
	ToolRegistry          *tools.Registry
	ToolEngine            *tools.Engine
	ToolAudit             audit.Store
	AIResourceHandler     *airesource.HTTPHandler
	AIResources           *airesource.Service
	AgentDefinitions      *agentcatalog.Service
	Collaboration         *collaboration.Service
	CodingService         *coding.Service
	APIToken              string
}

func NewRouter(deps Dependencies) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	if deps.APIToken != "" {
		router.Use(func(c *gin.Context) {
			// The Dashboard has its own short-lived browser session; API routes
			// continue to require the Bearer token.
			if strings.HasPrefix(c.Request.URL.Path, "/dashboard") {
				c.Next()
				return
			}
			if c.Request.Method == http.MethodGet && (c.Request.URL.Path == "/workflow-editor" || c.Request.URL.Path == "/workflow-editor/app.js") {
				c.Next()
				return
			}
			parts := strings.Fields(c.GetHeader("Authorization"))
			token := ""
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				token = parts[1]
			}
			if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(deps.APIToken)) != 1 {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "valid Bearer API token required"}})
				return
			}
			c.Next()
		})
	}
	router.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
		c.Next()
	})
	registerWorkflowEditor(router)
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
	if deps.DB != nil {
		handlers.NewExecutionEventsHandler(deps.DB).RegisterRoutes(api)
		handlers.NewUnifiedExecutionHandler(deps.DB).RegisterRoutes(api)
	}
	if deps.CodingService != nil {
		handlers.NewCodingHandler(deps.CodingService).RegisterRoutes(api)
	}
	h := handlers.NewCatalogHandler(deps.Providers, deps.Models, deps.Adapters, deps.ProviderRuntime, deps.Repository, deps.Gateway)
	if deps.AIResources != nil {
		h.SetDynamicModelCatalog(deps.AIResources)
	}
	h.RegisterRoutes(api)
	if deps.CredentialManager != nil {
		handlers.NewCredentialHandler(deps.CredentialManager).RegisterRoutes(api)
	}
	if deps.AgentExecutor != nil {
		handlers.NewExecutionHandler(deps.AgentExecutor).RegisterRoutes(api)
	}
	if deps.ExecutionManager != nil {
		handlers.NewManagedExecutionHandler(deps.ExecutionManager).RegisterRoutes(api)
	}
	if deps.Workspaces != nil {
		handlers.NewWorkspaceHandler(deps.Workspaces).RegisterRoutes(api)
	}
	if deps.AgentRuntime != nil {
		agentHandler := handlers.NewAgentHandler(deps.AgentRuntime, deps.SchedulerStore)
		agentHandler.SetMaxAttempts(deps.SchedulerMaxAttempts)
		agentHandler.RegisterRoutes(api)
	}
	if deps.AgentDefinitions != nil {
		handlers.NewAgentDefinitionHandler(deps.AgentDefinitions).RegisterRoutes(api)
	}
	if deps.GoalService != nil && deps.PlanningService != nil {
		handlers.NewGoalHandler(deps.GoalService, deps.PlanningService).RegisterRoutes(api)
	}
	if deps.PlanExecutionService != nil {
		handlers.NewPlanExecutionHandler(deps.PlanExecutionService).RegisterRoutes(api)
	}
	if deps.GoalEvaluationService != nil {
		handlers.NewGoalEvaluationHandler(deps.GoalEvaluationService).RegisterRoutes(api)
	}
	if deps.ReflectionService != nil {
		handlers.NewReflectionHandler(deps.ReflectionService).RegisterRoutes(api)
	}
	if deps.MemoryService != nil {
		handlers.NewMemoryHandler(deps.MemoryService).RegisterRoutes(api)
	}
	if deps.WorkflowEngine != nil {
		workflowHandler := handlers.NewWorkflowHandler(deps.WorkflowEngine)
		workflowHandler.SetMaxAttempts(deps.WorkflowMaxAttempts)
		workflowHandler.RegisterRoutes(api)
	}
	if deps.Collaboration != nil {
		collaborationHandler := handlers.NewCollaborationHandler(deps.Collaboration)
		collaborationHandler.SetRuntimeBindingDependencies(deps.AgentDefinitions, deps.Workspaces)
		collaborationHandler.RegisterRoutes(api)
		handlers.NewArtifactProvenanceHandler(deps.Collaboration).RegisterRoutes(api)
	}
	if deps.DB != nil {
		if repo, err := conversation.NewSQLiteRepository(deps.DB); err == nil {
			if service, serviceErr := conversation.NewService(repo); serviceErr == nil {
				handlers.NewConversationHandler(service).RegisterRoutes(api)
			}
			var memoryRetriever contextmanager.MemoryRetriever
			if deps.MemoryService != nil {
				memoryRetriever = deps.MemoryService
			}
			assembler := contextmanager.NewAssembler(repo, memoryRetriever, deps.Gateway)
			handlers.NewConversationContextHandler(assembler).RegisterRoutes(api)
		}
		if authoringHandler, err := handlers.NewWorkflowAuthoringHandler(deps.DB, deps.WorkflowEngine); err == nil {
			authoringHandler.RegisterRoutes(api)
		}
	}
	if deps.ToolRegistry != nil && deps.ToolEngine != nil && deps.Workspaces != nil && deps.ToolAudit != nil {
		handlers.NewToolHandler(deps.ToolEngine, deps.ToolRegistry, deps.Workspaces, deps.ToolAudit).RegisterRoutes(api)
	}
	if deps.AIResourceHandler != nil {
		deps.AIResourceHandler.RegisterRoutes(router)
	}
	dashboard.New(dashboard.Dependencies{DB: deps.DB, APIToken: deps.APIToken, ExecutionManager: deps.ExecutionManager, Providers: deps.Providers, Models: deps.Models, Credentials: deps.CredentialManager, AgentDefinitions: deps.AgentDefinitions, Catalog: h}).RegisterRoutes(router)
	return router
}
