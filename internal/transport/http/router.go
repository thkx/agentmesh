package http

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"context"
	"crypto/subtle"
	"database/sql"
	"net/http"
	"runtime/debug"
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
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"log/slog"

	"github.com/thkx/agentmesh/internal/observability"
	"github.com/thkx/agentmesh/internal/operations/diagnostics"
	"github.com/thkx/agentmesh/internal/platform/logger"
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
	Logger                *slog.Logger
	Metrics               *observability.Metrics
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
	Alerts                *observability.AlertEvaluator
	RequestShutdown       chan<- struct{}
	LogBuffer             *logger.RingBuffer
}

func NewRouter(deps Dependencies) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	metrics := deps.Metrics
	if metrics == nil {
		metrics = observability.NewMetrics()
	}
	router.Use(requestCorrelationMiddleware(deps.Logger))
	router.Use(requestMetricsMiddleware(metrics))
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
	router.GET("/metrics", gin.WrapH(metrics.Handler()))
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
	alerts := deps.Alerts
	if alerts == nil { alerts = observability.NewAlertEvaluator(100) }
	api := router.Group("/api/v1")
	api.GET("/operations/health", func(c *gin.Context) {
		snapshot := operationalAlertSnapshot(c.Request.Context(), deps.DB, metrics)
		m := metrics.Snapshot()
		status := http.StatusOK
		if !snapshot.DatabaseAvailable || !snapshot.WorkerRunning { status = http.StatusServiceUnavailable }
		c.JSON(status, gin.H{"database_available": snapshot.DatabaseAvailable, "worker_running": snapshot.WorkerRunning, "runtime_workers": m.RuntimeWorkers, "active_executions": m.ActiveExecutions, "execution_backlog": m.ExecutionBacklog, "provider_requests": snapshot.ProviderRequests, "provider_failures": snapshot.ProviderFailures, "usage_write_failures": m.UsageWriteFailures, "uptime_seconds": m.ProcessUptimeSeconds, "observed_at": snapshot.ObservedAt})
	})
	api.POST("/operations/shutdown", func(c *gin.Context) {
		if deps.RequestShutdown == nil { c.JSON(http.StatusServiceUnavailable, gin.H{"error":"graceful shutdown is not configured"}); return }
		select { case deps.RequestShutdown <- struct{}{}: c.JSON(http.StatusAccepted, gin.H{"status":"shutdown_requested"}); default: c.JSON(http.StatusConflict, gin.H{"status":"shutdown_already_requested"}) }
	})
	api.GET("/operations/alerts", func(c *gin.Context) {
		snapshot := operationalAlertSnapshot(c.Request.Context(), deps.DB, metrics)
		active := alerts.Evaluate(snapshot)
		c.JSON(http.StatusOK, gin.H{"active": active, "history": alerts.History(), "observed_at": snapshot.ObservedAt})
	})
	api.GET("/diagnostics/report", func(c *gin.Context) {
		dbReady := false
		if deps.DB != nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
			defer cancel()
			dbReady = deps.DB.PingContext(ctx) == nil
		}
		version := "development"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
			version = info.Main.Version
		}
		snap := metrics.Snapshot()
		report := diagnostics.Build(diagnostics.Input{Version: version, StartedAt: time.Now().Add(-time.Duration(snap.ProcessUptimeSeconds * float64(time.Second))), DatabaseReady: dbReady, RecentLogs: recentLogSnapshot(deps.LogBuffer), Metrics: map[string]int64{"http_requests": int64(snap.HTTPRequestCount), "model_requests": int64(snap.ModelGatewayCount), "http_in_flight": snap.HTTPRequestsInFlight}}, time.Now().UTC())
		c.JSON(http.StatusOK, report)
	})
	api.GET("/diagnostics/bundle", func(c *gin.Context) {
		snapshot := operationalAlertSnapshot(c.Request.Context(), deps.DB, metrics)
		m := metrics.Snapshot()
		version := "development"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" { version = info.Main.Version }
		report := diagnostics.Build(diagnostics.Input{Version:version,StartedAt:time.Now().Add(-time.Duration(m.ProcessUptimeSeconds*float64(time.Second))),DatabaseReady:snapshot.DatabaseAvailable,WorkerCount:int(m.RuntimeWorkers),RecentLogs:recentLogSnapshot(deps.LogBuffer),Metrics:map[string]int64{"http_requests":int64(m.HTTPRequestCount),"model_requests":int64(m.ModelGatewayCount),"http_in_flight":m.HTTPRequestsInFlight,"execution_backlog":m.ExecutionBacklog,"runtime_workers":m.RuntimeWorkers,"active_executions":m.ActiveExecutions,"usage_write_failures":int64(m.UsageWriteFailures)}},time.Now().UTC())
		reportJSON,_:=json.MarshalIndent(report,"","  ")
		var metricsBuffer bytes.Buffer
		metrics.Handler().ServeHTTP(&metricsResponseWriter{header:make(http.Header),body:&metricsBuffer},c.Request)
		healthJSON,_:=json.MarshalIndent(gin.H{"database_available":snapshot.DatabaseAvailable,"worker_running":snapshot.WorkerRunning,"runtime_workers":m.RuntimeWorkers,"active_executions":m.ActiveExecutions,"execution_backlog":m.ExecutionBacklog,"provider_failures":m.ProviderFailures,"usage_write_failures":m.UsageWriteFailures},"","  ")
		var bundle bytes.Buffer
		zw:=zip.NewWriter(&bundle)
		for name,data:=range map[string][]byte{"diagnostics.json":reportJSON,"metrics.prom":metricsBuffer.Bytes(),"health.json":healthJSON} {
			entry,err:=zw.Create(name);if err!=nil{c.Status(http.StatusInternalServerError);return}
			if _,err=entry.Write(data);err!=nil{c.Status(http.StatusInternalServerError);return}
		}
		if err:=zw.Close();err!=nil{c.Status(http.StatusInternalServerError);return}
		c.Header("Content-Type","application/zip")
		c.Header("Content-Disposition",`attachment; filename="agentmesh-support.zip"`)
		c.Header("Cache-Control","no-store")
		c.Data(http.StatusOK,"application/zip",bundle.Bytes())
	})
	if deps.DB != nil {
		handlers.NewExecutionEventsHandler(deps.DB).RegisterRoutes(api)
		handlers.NewUnifiedExecutionHandler(deps.DB).RegisterRoutes(api)
		handlers.NewUsageHandler(deps.DB).RegisterRoutes(api)
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
	dashboard.New(dashboard.Dependencies{DB: deps.DB, Metrics: metrics, Alerts: alerts, APIToken: deps.APIToken, ExecutionManager: deps.ExecutionManager, Providers: deps.Providers, Models: deps.Models, Credentials: deps.CredentialManager, AgentDefinitions: deps.AgentDefinitions, Collaboration: deps.Collaboration, WorkflowEngine: deps.WorkflowEngine, WorkflowMaxAttempts: deps.WorkflowMaxAttempts, Catalog: h}).RegisterRoutes(router)
	return otelhttp.NewHandler(router, "agentmesh.http")
}

// operationalAlertSnapshot reads only bounded operational facts; query failures never expose raw SQL errors.
func operationalAlertSnapshot(ctx context.Context, db *sql.DB, metrics *observability.Metrics) observability.AlertSnapshot {
	s := observability.AlertSnapshot{ObservedAt: time.Now().UTC()}
	if db != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		s.DatabaseAvailable = db.PingContext(pingCtx) == nil
		cancel()
		var active, backlog int64
		_ = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN status='running' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN status IN ('queued','reconciling') THEN 1 ELSE 0 END),0) FROM executions`).Scan(&active, &backlog)
		if metrics != nil { metrics.SetExecutionState(active, backlog) }
		s.ReconciliationBacklog = backlog
	}
	if metrics != nil {
		m := metrics.Snapshot()
		s.WorkerRunning = m.RuntimeWorkers > 0
		s.ProviderRequests = m.ModelGatewayCount
		s.ProviderFailures = m.ProviderFailures
		s.ReconciliationBacklog = m.ExecutionBacklog
	}
	return s
}

type metricsResponseWriter struct { header http.Header; body *bytes.Buffer; status int }
func (w *metricsResponseWriter) Header() http.Header { return w.header }
func (w *metricsResponseWriter) WriteHeader(status int) { w.status=status }
func (w *metricsResponseWriter) Write(data []byte) (int,error) { return w.body.Write(data) }

func recentLogSnapshot(buffer *logger.RingBuffer) []string { if buffer == nil { return nil }; return buffer.Snapshot() }
