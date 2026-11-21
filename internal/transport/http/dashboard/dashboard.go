package dashboard

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/agentcatalog"
	"github.com/thkx/agentmesh/internal/coding"
	"github.com/thkx/agentmesh/internal/collaboration"
	appcredential "github.com/thkx/agentmesh/internal/credential"
	domainagent "github.com/thkx/agentmesh/internal/domain/agent"
	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	execdom "github.com/thkx/agentmesh/internal/domain/execution"
	domainprovider "github.com/thkx/agentmesh/internal/domain/provider"
	domainworkflow "github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/execution/manager"
	"github.com/thkx/agentmesh/internal/observability"
	"github.com/thkx/agentmesh/internal/transport/http/handlers"
	"github.com/thkx/agentmesh/internal/workflow/authoring"
	workflowruntime "github.com/thkx/agentmesh/internal/workflow/runtime"
	"github.com/thkx/agentmesh/internal/workspace"
	webassets "github.com/thkx/agentmesh/web"
)

const sessionCookie = "agentmesh_dashboard"

type Dependencies struct {
	Metrics               *observability.Metrics
	Alerts                *observability.AlertEvaluator
	DB                    *sql.DB
	APIToken              string
	RequestShutdown       chan<- struct{}
	ExecutionManager      *manager.Manager
	CodingService         *coding.Service
	SandboxImage          string
	SandboxNetworkEnabled bool
	Providers             *domainprovider.ProviderRegistry
	Models                *domainprovider.ModelRegistry
	Credentials           appcredential.Manager
	AgentDefinitions      *agentcatalog.Service
	Collaboration         *collaboration.Service
	WorkflowEngine        *workflowruntime.Engine
	WorkflowMaxAttempts   int
	Workspaces            *workspace.Manager
	Catalog               *handlers.CatalogHandler
}

type Handler struct {
	metrics               *observability.Metrics
	alerts                *observability.AlertEvaluator
	db                    *sql.DB
	token                 string
	requestShutdown       chan<- struct{}
	manager               *manager.Manager
	codingService         *coding.Service
	sandboxImage          string
	sandboxNetworkEnabled bool
	providerRegistry      *domainprovider.ProviderRegistry
	modelRegistry         *domainprovider.ModelRegistry
	credentials           appcredential.Manager
	agentCatalog          *agentcatalog.Service
	collaboration         *collaboration.Service
	workflowEngine        *workflowruntime.Engine
	workflowMaxAttempts   int
	workspaces            *workspace.Manager
	workflowRepo          *authoring.Repository
	catalogAPI            http.Handler
	executionAPI          http.Handler
	tmpl                  *template.Template
	loginMu               sync.Mutex
	loginAttempts         map[string][]time.Time
}

type workspaceOption struct{ ID, Name string }

type setupStepView struct {
	Title       string
	Description string
	Href        string
	Done        bool
}

type pageData struct {
	Title                       string
	Active                      string
	SetupSteps                  []setupStepView
	NextSetupStep               setupStepView
	SetupDone                   int
	SetupTotal                  int
	Error                       string
	Message                     string
	Readiness                   string
	Counts                      map[string]int64
	Executions                  []executionRow
	CodingRows                  []coding.Task
	CodingTask                  *coding.Task
	CodingSteps                 []coding.Step
	CodingEvents                []coding.Event
	CodingSnapshots             []coding.Snapshot
	CodingDiff                  string
	CodingSandboxImage          string
	CodingSandboxNetworkEnabled bool
	Execution                   *executionDetail
	ProviderRows                []providerView
	ModelRows                   []modelView
	AgentRows                   []agentView
	IdentityRows                []identityView
	TaskRows                    []taskView
	WorkspaceOptions            []workspaceOption
	ProviderOptions             []providerOption
	ModelOptions                []modelOption
	AllProviderOptions          []providerOption
	AllModelOptions             []modelOption
	WorkflowRows                []workflowView
	WorkflowRuns                []workflowRunListView
	WorkflowRun                 *workflowRunDetailView
	AgentRun                    *agentRunDetailView
	Credential                  *credentialView
	FilterStatus                string
	FilterKind                  string
	FilterRuntime               string
	FilterTask                  string
	Offset                      int
	Limit                       int
	PrevOffset                  int
	NextOffset                  int
	HasPrevious                 bool
	HasNext                     bool
	Attempts                    []attemptView
	Artifacts                   []artifactView
	Evidence                    []evidenceView
	Events                      []eventView
	UsageRows                   []usageView
	UsageEvents                 []usageEventView
	UsageSince                  string
	UsageUntil                  string
	UsageLimit                  int
	UsageOffset                 int
	UsageNextOffset             int
	UsagePrevOffset             int
	UsageHasMore                bool
	TimelineHasMore             bool
	TimelineNextURL             string
	RunResult                   string
	ActiveAlerts                []observability.Alert
	AlertHistory                []observability.Alert
	OpsHealth                   operationsHealthView
}

type operationsHealthView struct {
	Version           string
	DatabaseReady     bool
	DatabaseIntegrity string
	SchemaVersion     uint
	MigrationDirty    bool
	DatabaseSizeBytes int64
	WorkerStatus      string
}

type providerView struct{ ID, Name, Type, Kind, BaseURL, Status, CredentialStatus string }
type providerOption struct{ ID, Name string }
type modelOption struct{ ID, Name string }
type modelView struct {
	ID, ProviderID, Name, DisplayName, Status, Source string
	ContextWindow, MaxOutputTokens                    int
	Streaming                                         bool
}
type agentRunView struct{ ID, TaskID, Status, CreatedAt, ExecutionID string }
type agentRunDetailView struct {
	ID, DefinitionID, AgentName, TaskID, Status string
	Error, Content, CreatedAt, StartedAt, FinishedAt, Duration, ExecutionID string
	Steps, ToolCalls int
}
type agentOption struct{ ID, Name, Role, Status, Capabilities string }
type identityView struct{ ID, WorkspaceID, DefinitionID, Name, Description, Role, Status, Capabilities string }
type taskView struct {
	TaskID, WorkspaceID, ParentTaskID, Title, Type, Status, CreatedAt, ExecutionID string
	Priority                                                                       int
	AgentOptions                                                                   []agentOption
}
type workflowRunView struct {
	ID, Status, CreatedAt, ExecutionID string
	Version                            int
}

type agentView struct {
	ID, Name, ModelID, Description, SystemPrompt, ApprovedTools string
	Temperature                                                 float64
	MaxSteps, MaxToolCalls, MaxOutputTokens, TimeoutSeconds     int
	Runs                                                        []agentRunView
	Enabled                                                     bool
	Version                                                     int
}
type workflowView struct {
	ID, Name, Description string
	Version               int
	UpdatedAt             string
	Runs                  []workflowRunView
}

type workflowRunListView struct {
	ID, WorkflowID, WorkflowName, Status, CreatedAt, UpdatedAt string
	Version                                                    int
}

type workflowNodeView struct {
	ID, Status, StartedAt, FinishedAt string
	Attempt                           int
	DurationMillis                    int64
}

type workflowEventView struct {
	Sequence                 int64
	Type, NodeID, OccurredAt string
}

type workflowRunDetailView struct {
	ID, WorkflowID, WorkflowName, Status, CurrentNode, CreatedAt, UpdatedAt, FinishedAt string
	Version                                                                             int
	Nodes                                                                               []workflowNodeView
	Events                                                                              []workflowEventView
}

type credentialView struct {
	Source, Status string
	UpdatedAt      string
}
type attemptView struct {
	ID, Runtime, Status string
	Number              int
	StartedAt           string
	FinishedAt          string
}
type artifactView struct {
	ID, ArtifactID, SourceExecutionID, SourceAttemptID string
	Version                                            int
	MediaType, Hash                                    string
	Size                                               int64
}
type evidenceView struct{ ID, ArtifactID, VersionID, Kind, Statement, ExecutionID, AttemptID string }
type usageView struct {
	ProviderID, ModelID, Currency                   string
	Requests, InputTokens, OutputTokens, CostMicros int64
}
type usageEventView struct {
	ID, OccurredAt, ProviderID, ModelID, Source, Currency, ExecutionID, AttemptID string
	InputTokens, OutputTokens, CostMicros                                         int64
}
type eventView struct {
	Stream, Type, Actor, Source, Payload, OccurredAt string
	Sequence                                         int64
	OccurredNS                                       int64
}

type executionRow struct {
	ID, Kind, Runtime, Status, TaskID string
	CreatedAt, UpdatedAt              string
}

type executionDetail struct {
	ID, Kind, Runtime, Status, TaskID string
	CreatedAt, UpdatedAt              string
}

func New(d Dependencies) *Handler {
	metrics := d.Metrics
	if metrics == nil {
		metrics = observability.NewMetrics()
	}
	t := template.Must(template.ParseFS(webassets.Assets, "dashboard/templates/*.html"))
	h := &Handler{
		db:                    d.DB,
		metrics:               metrics,
		alerts:                d.Alerts,
		token:                 d.APIToken,
		requestShutdown:       d.RequestShutdown,
		manager:               d.ExecutionManager,
		codingService:         d.CodingService,
		sandboxImage:          d.SandboxImage,
		sandboxNetworkEnabled: d.SandboxNetworkEnabled,
		providerRegistry:      d.Providers,
		modelRegistry:         d.Models,
		credentials:           d.Credentials,
		agentCatalog:          d.AgentDefinitions,
		collaboration:         d.Collaboration,
		workflowEngine:        d.WorkflowEngine,
		workflowMaxAttempts:   d.WorkflowMaxAttempts,
		workspaces:            d.Workspaces,
		tmpl:                  t,
		loginAttempts:         make(map[string][]time.Time),
	}
	if d.DB != nil {
		h.workflowRepo, _ = authoring.NewRepository(d.DB)
		executionRouter := gin.New()
		handlers.NewUnifiedExecutionHandler(d.DB).RegisterRoutes(executionRouter.Group("/api/v1"))
		h.executionAPI = executionRouter
	}
	if d.Catalog != nil {
		catalogRouter := gin.New()
		d.Catalog.RegisterRoutes(catalogRouter.Group("/api/v1"))
		h.catalogAPI = catalogRouter
	}
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	g := r.Group("/dashboard")
	g.GET("/assets/htmx.min.js", h.htmxAsset)
	g.GET("/assets/app.css", h.cssAsset)
	g.GET("/login", h.loginPage)
	g.POST("/login", h.login)
	g.POST("/logout", h.requireSession(), h.logout)
	g.GET("", h.requireSession(), h.overview)
	g.GET("/", h.requireSession(), h.overview)
	g.GET("/new-task", h.requireSession(), h.newTask)
	g.POST("/new-task/agent", h.requireSession(), h.submitNewAgentTask)
	g.GET("/providers", h.requireSession(), h.providers)
	g.POST("/providers", h.requireSession(), h.createProvider)
	g.POST("/providers/:id/update", h.requireSession(), h.updateProvider)
	g.POST("/providers/:id/enabled", h.requireSession(), h.setProviderEnabled)
	g.POST("/providers/:id/delete", h.requireSession(), h.deleteProvider)
	g.POST("/providers/:id/test", h.requireSession(), h.testProvider)
	g.POST("/providers/:id/credential", h.requireSession(), h.setCredential)
	g.POST("/providers/:id/credential/delete", h.requireSession(), h.deleteCredential)
	g.GET("/models", h.requireSession(), h.models)
	g.POST("/models", h.requireSession(), h.createModel)
	g.POST("/models/:id/update", h.requireSession(), h.updateModel)
	g.POST("/models/:id/enabled", h.requireSession(), h.setModelEnabled)
	g.POST("/models/:id/delete", h.requireSession(), h.deleteModel)
	g.GET("/agents", h.requireSession(), h.agents)
	g.GET("/tasks", h.requireSession(), h.tasks)
	g.POST("/tasks", h.requireSession(), h.createTask)
	g.POST("/tasks/:id/assign", h.requireSession(), h.assignTask)
	g.POST("/agents", h.requireSession(), h.createAgent)
	g.POST("/agent-identities", h.requireSession(), h.createAgentIdentity)
	g.POST("/agent-identities/:id/enable", h.requireSession(), h.enableAgentIdentity)
	g.POST("/agent-identities/:id/disable", h.requireSession(), h.disableAgentIdentity)
	g.POST("/agent-identities/:id/delete", h.requireSession(), h.deleteAgentIdentity)
	g.POST("/agents/:id/update", h.requireSession(), h.updateAgent)
	g.POST("/agents/:id/run", h.requireSession(), h.runAgent)
	g.POST("/agents/:id/enabled", h.requireSession(), h.setAgentEnabled)
	g.POST("/agents/:id/delete", h.requireSession(), h.deleteAgent)
	g.GET("/workflows", h.requireSession(), h.workflows)
	g.GET("/workflow-runs/:id", h.requireSession(), h.workflowRun)
	g.GET("/agent-runs/:id", h.requireSession(), h.agentRun)
	g.POST("/workflows", h.requireSession(), h.createWorkflowTemplate)
	g.POST("/workspaces/quickstart", h.requireSession(), h.createQuickstartWorkspace)
	g.POST("/workflows/quickstart", h.requireSession(), h.createQuickstartWorkflowTemplate)
	g.POST("/workflows/:id/apply", h.requireSession(), h.applyWorkflowTemplate)
	g.POST("/workflows/:id/run", h.requireSession(), h.runWorkflow)
	g.POST("/workflows/:id/delete", h.requireSession(), h.deleteWorkflowTemplate)
	g.GET("/operations/metrics", h.requireSession(), h.operationsMetrics)
	g.POST("/operations/shutdown", h.requireSession(), h.requestGracefulShutdown)
	g.GET("/usage", h.requireSession(), h.usage)
	g.GET("/coding", h.requireSession(), h.codingTasks)
	g.POST("/coding", h.requireSession(), h.createCodingTask)
	g.GET("/coding/:id", h.requireSession(), h.codingTask)
	g.POST("/coding/:id/cancel", h.requireSession(), h.cancelCodingTask)
	g.POST("/coding/:id/resolve", h.requireSession(), h.resolveCodingTask)
	g.POST("/coding/:id/revert", h.requireSession(), h.revertCodingTask)
	g.POST("/coding/:id/cleanup", h.requireSession(), h.cleanupCodingTask)
	g.GET("/executions", h.requireSession(), h.executions)
	g.GET("/executions/:id", h.requireSession(), h.execution)
	g.POST("/executions/:id/cancel", h.requireSession(), h.cancelExecution)
}

func (h *Handler) htmxAsset(c *gin.Context) {
	asset, err := webassets.Assets.ReadFile("dashboard/static/vendor/htmx.min.js")
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(http.StatusOK, "text/javascript; charset=utf-8", asset)
}

func (h *Handler) cssAsset(c *gin.Context) {
	asset, err := webassets.Assets.ReadFile("dashboard/static/css/app.css")
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(http.StatusOK, "text/css; charset=utf-8", asset)
}

func (h *Handler) requireSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.token == "" || h.validSession(c) {
			c.Next()
			return
		}
		if c.Request.Method == http.MethodGet {
			c.Redirect(http.StatusSeeOther, "/dashboard/login")
		} else {
			c.AbortWithStatus(http.StatusUnauthorized)
		}
		c.Abort()
	}
}

func (h *Handler) loginPage(c *gin.Context) {
	if h.token == "" {
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	h.render(c, pageData{Title: "Sign in"}, http.StatusOK)
}

func (h *Handler) allowLogin(remoteAddr string) bool {
	now := time.Now()
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	cutoff := now.Add(-10 * time.Minute)
	attempts := h.loginAttempts[remoteAddr]
	kept := attempts[:0]
	for _, attempt := range attempts {
		if attempt.After(cutoff) {
			kept = append(kept, attempt)
		}
	}
	if len(kept) >= 5 {
		h.loginAttempts[remoteAddr] = kept
		return false
	}
	h.loginAttempts[remoteAddr] = append(kept, now)
	return true
}

func (h *Handler) clearLoginAttempts(remoteAddr string) {
	h.loginMu.Lock()
	delete(h.loginAttempts, remoteAddr)
	h.loginMu.Unlock()
}

func (h *Handler) login(c *gin.Context) {
	if h.token == "" {
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	if !sameOrigin(c.Request) || (!isLoopbackRequest(c.Request) && c.Request.TLS == nil) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if !h.allowLogin(remoteHost(c.Request)) {
		c.AbortWithStatus(http.StatusTooManyRequests)
		return
	}
	provided := c.PostForm("token")
	if len(provided) != len(h.token) || subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) != 1 {
		h.render(c, pageData{Title: "Sign in", Error: "Invalid credentials"}, http.StatusUnauthorized)
		return
	}

	expires := time.Now().Add(8 * time.Hour).Unix()
	value := strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, []byte(h.token))
	_, _ = mac.Write([]byte("agentmesh-dashboard-session:v1:" + value))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	h.clearLoginAttempts(remoteHost(c.Request))
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    value + "." + sig,
		Path:     "/dashboard",
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(expires, 0),
		MaxAge:   8 * 60 * 60,
	})
	c.Redirect(http.StatusSeeOther, "/dashboard")
}

func (h *Handler) validSession(c *gin.Context) bool {
	cookie, err := c.Request.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expires <= time.Now().Unix() {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(h.token))
	_, _ = mac.Write([]byte("agentmesh-dashboard-session:v1:" + parts[0]))
	return hmac.Equal(got, mac.Sum(nil))
}

func (h *Handler) logout(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/dashboard",
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	c.Redirect(http.StatusSeeOther, "/dashboard/login")
}

func (h *Handler) requestGracefulShutdown(c *gin.Context) {
	if !sameOrigin(c.Request) || c.PostForm("confirm") != "yes" {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.requestShutdown == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	select {
	case h.requestShutdown <- struct{}{}:
		c.Redirect(http.StatusSeeOther, "/dashboard/operations/metrics?message=shutdown-requested")
	default:
		c.Redirect(http.StatusSeeOther, "/dashboard/operations/metrics?error=shutdown")
	}
}

func (h *Handler) operationsMetrics(c *gin.Context) {
	snapshot := observability.AlertSnapshot{ObservedAt: time.Now().UTC()}
	data := pageData{Title: "Operations Metrics", Active: "operations", OpsHealth: operationsHealthView{DatabaseIntegrity: "unknown"}}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		data.OpsHealth.Version = info.Main.Version
	} else {
		data.OpsHealth.Version = "development"
	}
	if h.db != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		snapshot.DatabaseAvailable = h.db.PingContext(ctx) == nil
		data.OpsHealth.DatabaseReady = snapshot.DatabaseAvailable
		var version uint
		var dirty bool
		if err := h.db.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&version, &dirty); err == nil {
			data.OpsHealth.SchemaVersion = version
			data.OpsHealth.MigrationDirty = dirty
		}
		var integrity string
		if err := h.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err == nil {
			if strings.EqualFold(strings.TrimSpace(integrity), "ok") {
				data.OpsHealth.DatabaseIntegrity = "ok"
			} else {
				data.OpsHealth.DatabaseIntegrity = "failed"
			}
		}
		if rows, err := h.db.QueryContext(ctx, "PRAGMA database_list"); err == nil {
			for rows.Next() {
				var sequence int
				var name, file string
				if rows.Scan(&sequence, &name, &file) == nil && name == "main" && file != "" {
					if info, statErr := os.Stat(file); statErr == nil && info.Mode().IsRegular() {
						data.OpsHealth.DatabaseSizeBytes = info.Size()
					}
				}
			}
			_ = rows.Close()
		}
		cancel()
		var active, backlog int64
		_ = h.db.QueryRowContext(c.Request.Context(), `SELECT COALESCE(SUM(CASE WHEN status='running' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN status IN ('queued','reconciling') THEN 1 ELSE 0 END),0) FROM executions`).Scan(&active, &backlog)
		h.metrics.SetExecutionState(active, backlog)
	}
	m := h.metrics.Snapshot()
	if m.RuntimeWorkers > 0 {
		data.OpsHealth.WorkerStatus = "running"
	} else {
		data.OpsHealth.WorkerStatus = "stopped"
	}
	snapshot.WorkerRunning = m.RuntimeWorkers > 0
	snapshot.ProviderRequests = m.ProviderRequests
	snapshot.ProviderFailures = m.ProviderFailures
	snapshot.UsageWriteFailures = m.UsageWriteFailures
	snapshot.ReconciliationBacklog = m.ExecutionBacklog
	var activeAlerts []observability.Alert
	var alertHistory []observability.Alert
	if h.alerts != nil {
		activeAlerts = h.alerts.Evaluate(snapshot)
		alertHistory = h.alerts.History()
	}
	data.Counts = map[string]int64{
		"HTTP requests":          int64(m.HTTPRequestCount),
		"Model Gateway requests": int64(m.ModelGatewayCount),
		"HTTP in flight":         m.HTTPRequestsInFlight,
		"Runtime workers":        m.RuntimeWorkers,
		"Active executions":      m.ActiveExecutions,
		"Execution backlog":      m.ExecutionBacklog,
		"Usage write failures":   int64(m.UsageWriteFailures),
		"Process uptime seconds": int64(m.ProcessUptimeSeconds),
	}
	data.ActiveAlerts = activeAlerts
	data.AlertHistory = alertHistory
	h.render(c, data, http.StatusOK)
}

func (h *Handler) usage(c *gin.Context) {
	now := time.Now().UTC()
	since := now.Add(-30 * 24 * time.Hour)
	if raw := c.Query("since"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			since = parsed.UTC()
		} else {
			h.render(c, pageData{Title: "Model Usage & Cost", Active: "usage", Error: "since must be RFC3339"}, http.StatusBadRequest)
			return
		}
	}
	until := now
	if raw := c.Query("until"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			until = parsed.UTC()
		} else {
			h.render(c, pageData{Title: "Model Usage & Cost", Active: "usage", Error: "until must be RFC3339"}, http.StatusBadRequest)
			return
		}
	}
	if !until.After(since) {
		h.render(c, pageData{Title: "Model Usage & Cost", Active: "usage", Error: "until must be later than since"}, http.StatusBadRequest)
		return
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 200 {
			limit = n
		} else {
			h.render(c, pageData{Title: "Model Usage & Cost", Active: "usage", Error: "limit must be between 1 and 200"}, http.StatusBadRequest)
			return
		}
	}
	offset := 0
	if raw := c.Query("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= 1000000 {
			offset = n
		} else {
			h.render(c, pageData{Title: "Model Usage & Cost", Active: "usage", Error: "offset must be between 0 and 1000000"}, http.StatusBadRequest)
			return
		}
	}
	data := pageData{Title: "Model Usage & Cost", Active: "usage", Counts: map[string]int64{}, UsageSince: since.Format(time.RFC3339), UsageUntil: until.Format(time.RFC3339), UsageLimit: limit, UsageOffset: offset, UsagePrevOffset: max(0, offset-limit), UsageNextOffset: offset + limit}
	if h.db != nil {
		var requestCount, inputTokens, outputTokens int64
		_ = h.db.QueryRowContext(c.Request.Context(), `SELECT COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0) FROM model_usage_events WHERE occurred_at>=? AND occurred_at<?`, since.Format(time.RFC3339Nano), until.Format(time.RFC3339Nano)).Scan(&requestCount, &inputTokens, &outputTokens)
		data.Counts["Requests"] = requestCount
		data.Counts["Input tokens"] = inputTokens
		data.Counts["Output tokens"] = outputTokens
		rows, err := h.db.QueryContext(c.Request.Context(), `SELECT provider_id,model_id,COALESCE(currency,''),COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(estimated_cost_micros),0) FROM model_usage_events WHERE occurred_at>=? AND occurred_at<? GROUP BY provider_id,model_id,currency ORDER BY SUM(total_tokens) DESC LIMIT 100`, since.Format(time.RFC3339Nano), until.Format(time.RFC3339Nano))
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var item usageView
				if rows.Scan(&item.ProviderID, &item.ModelID, &item.Currency, &item.Requests, &item.InputTokens, &item.OutputTokens, &item.CostMicros) == nil {
					data.UsageRows = append(data.UsageRows, item)
				}
			}
		}
		eventRows, eventErr := h.db.QueryContext(c.Request.Context(), `SELECT id,occurred_at,provider_id,model_id,usage_source,COALESCE(currency,''),COALESCE(input_tokens,0),COALESCE(output_tokens,0),COALESCE(estimated_cost_micros,0),COALESCE(execution_id,''),COALESCE(attempt_id,'') FROM model_usage_events WHERE occurred_at>=? AND occurred_at<? ORDER BY occurred_at DESC,id DESC LIMIT ? OFFSET ?`, since.Format(time.RFC3339Nano), until.Format(time.RFC3339Nano), limit+1, offset)
		if eventErr == nil {
			defer eventRows.Close()
			for eventRows.Next() {
				var item usageEventView
				if eventRows.Scan(&item.ID, &item.OccurredAt, &item.ProviderID, &item.ModelID, &item.Source, &item.Currency, &item.InputTokens, &item.OutputTokens, &item.CostMicros, &item.ExecutionID, &item.AttemptID) == nil {
					data.UsageEvents = append(data.UsageEvents, item)
				}
			}
			data.UsageHasMore = len(data.UsageEvents) > limit
			if data.UsageHasMore {
				data.UsageEvents = data.UsageEvents[:limit]
			}
		}
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) newTask(c *gin.Context) {
	data := pageData{Title: "Start a task", Active: "new-task", AgentRows: []agentView{}, WorkspaceOptions: []workspaceOption{}}
	enabledProviders := make(map[domainprovider.ID]bool)
	if h.providerRegistry != nil {
		for _, provider := range h.providerRegistry.List() {
			if provider.Enabled {
				enabledProviders[provider.ID] = true
			}
		}
	}
	enabledModels := make(map[string]bool)
	if h.modelRegistry != nil {
		for _, model := range h.modelRegistry.List() {
			if model.Enabled && enabledProviders[model.ProviderID] {
				enabledModels[model.ID] = true
			}
		}
	}
	if h.agentCatalog != nil {
		items, err := h.agentCatalog.List(c.Request.Context())
		if err != nil {
			data.Error = "Could not load Agents. Open the Agents page and check the configuration."
		} else {
			for _, item := range items {
				if !item.Enabled || !enabledModels[item.ModelID] {
					continue
				}
				data.AgentRows = append(data.AgentRows, agentView{ID: item.ID, Name: item.Name, ModelID: item.ModelID, Description: item.Description, Enabled: item.Enabled})
			}
		}
	}
	if h.collaboration != nil {
		workspaces, err := h.collaboration.ListWorkspaces(c.Request.Context())
		if err != nil {
			data.Error = "Could not load workspaces. Check the workspace configuration."
		} else {
			for _, item := range workspaces {
				data.WorkspaceOptions = append(data.WorkspaceOptions, workspaceOption{ID: item.ID, Name: item.Name})
			}
		}
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) submitNewAgentTask(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.agentCatalog == nil || h.workspaces == nil || h.modelRegistry == nil || h.providerRegistry == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	agentID := strings.TrimSpace(c.PostForm("agent_id"))
	workspaceID := strings.TrimSpace(c.PostForm("workspace_id"))
	prompt := strings.TrimSpace(c.PostForm("prompt"))
	if agentID == "" || workspaceID == "" || prompt == "" || len(prompt) > 10000 {
		c.Redirect(http.StatusSeeOther, "/dashboard/new-task?error=new-task-input")
		return
	}
	agent, err := h.agentCatalog.Get(c.Request.Context(), agentID)
	if err != nil || !agent.Enabled {
		c.Redirect(http.StatusSeeOther, "/dashboard/new-task?error=new-task-agent")
		return
	}
	model, ok := h.modelRegistry.Get(agent.ModelID)
	if !ok || !model.Enabled {
		c.Redirect(http.StatusSeeOther, "/dashboard/new-task?error=new-task-agent")
		return
	}
	provider, err := h.providerRegistry.Get(model.ProviderID)
	if err != nil || !provider.Enabled {
		c.Redirect(http.StatusSeeOther, "/dashboard/new-task?error=new-task-agent")
		return
	}
	if _, err = h.workspaces.Ensure(c.Request.Context(), workspaceID); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/new-task?error=new-task-workspace")
		return
	}
	result, err := h.agentCatalog.Run(c.Request.Context(), agentID, workspaceID, prompt)
	runResult, runErr := h.agentCatalog.Run(c.Request.Context(), agentID, workspaceID, prompt)
	if runResult.TaskID != "" {
		if runID := h.agentRunIDForTask(c.Request.Context(), runResult.TaskID); runID != "" {
			c.Redirect(http.StatusSeeOther, "/dashboard/agent-runs/"+url.PathEscape(runID))
			return
		}
	}
	if runErr != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/new-task?error=new-task-run")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/agents?message=agent-run-"+string(runResult.Status))
}

func (h *Handler) agentRunIDForTask(ctx context.Context, taskID string) string {
	if h.db == nil || taskID == "" {
		return ""
	}
	var id string
	if err := h.db.QueryRowContext(ctx, "SELECT id FROM agent_runs WHERE task_id=?", taskID).Scan(&id); err != nil {
		return ""
	}
	return id
}

func (h *Handler) agentRun(c *gin.Context) {
	if h.agentCatalog == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	run, err := h.agentCatalog.GetRun(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	detail := &agentRunDetailView{
		ID: run.ID, DefinitionID: run.DefinitionID, TaskID: run.TaskID,
		Status: run.Status, Error: run.Error,
		CreatedAt: run.CreatedAt.UTC().Format(time.RFC3339),
		ExecutionID: h.executionForTask(c.Request.Context(), run.TaskID),
	}
	if definition, getErr := h.agentCatalog.Get(c.Request.Context(), run.DefinitionID); getErr == nil {
		detail.AgentName = definition.Name
	}
	if run.FinishedAt != nil {
		detail.FinishedAt = run.FinishedAt.UTC().Format(time.RFC3339)
	}
	if run.Result != nil {
		detail.Content = run.Result.Content
		detail.Steps = run.Result.Steps
		detail.ToolCalls = run.Result.ToolCalls
		if !run.Result.StartedAt.IsZero() {
			detail.StartedAt = run.Result.StartedAt.UTC().Format(time.RFC3339)
		}
		if !run.Result.FinishedAt.IsZero() {
			detail.FinishedAt = run.Result.FinishedAt.UTC().Format(time.RFC3339)
		}
		if detail.Error == "" {
			detail.Error = run.Result.Error
		}
		if !run.Result.StartedAt.IsZero() && !run.Result.FinishedAt.IsZero() {
			detail.Duration = run.Result.FinishedAt.Sub(run.Result.StartedAt).Round(time.Millisecond).String()
		}
	}
	h.render(c, pageData{Title: "Agent Run " + run.ID, Active: "agents", AgentRun: detail}, http.StatusOK)
}

func (h *Handler) overview(c *gin.Context) {
	data := pageData{
		Title:  "Overview",
		Active: "overview",
		Counts: map[string]int64{}, Readiness: "Database unavailable",
	}
	if h.db != nil {
		if err := h.db.PingContext(c.Request.Context()); err == nil {
			data.Readiness = "Database ready"
		}
		queries := map[string]string{
			"executions":            "SELECT COUNT(*) FROM executions",
			"agent_tasks_completed": "SELECT COUNT(*) FROM agent_runs WHERE status='succeeded'",
			"agents":                "SELECT COUNT(*) FROM agent_definitions WHERE enabled=1",
			"providers":             "SELECT COUNT(*) FROM providers WHERE enabled=1",
			"models":                "SELECT COUNT(*) FROM models m JOIN providers p ON p.id=m.provider_id WHERE m.enabled=1 AND p.enabled=1",
			"workflows":             "SELECT COUNT(*) FROM workflows",
		}
		for name, query := range queries {
			var count int64
			if err := h.db.QueryRowContext(c.Request.Context(), query).Scan(&count); err == nil {
				data.Counts[name] = count
			}
		}
	}
	data.SetupSteps = []setupStepView{
		{Title: "Connect a model provider", Description: "Add the service or local model endpoint that AgentMesh will call.", Href: "/dashboard/providers", Done: data.Counts["providers"] > 0},
		{Title: "Enable a model", Description: "Register an available model and make sure its credentials are configured.", Href: "/dashboard/models", Done: data.Counts["models"] > 0},
		{Title: "Create your first Agent", Description: "Give an Agent a model, a role and a clear task description.", Href: "/dashboard/agents", Done: data.Counts["agents"] > 0},
		{Title: "Run your first Agent task", Description: "Submit a small task and inspect its Execution history.", Href: "/dashboard/agents", Done: data.Counts["agent_tasks_completed"] > 0},
		{Title: "Build a reusable Workflow", Description: "Combine Agents and other activities into a repeatable process.", Href: "/dashboard/workflows", Done: data.Counts["workflows"] > 0},
	}
	data.SetupTotal = len(data.SetupSteps)
	for _, step := range data.SetupSteps {
		if step.Done {
			data.SetupDone++
			continue
		}
		if data.NextSetupStep.Title == "" {
			data.NextSetupStep = step
		}
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) callCatalog(c *gin.Context, method, path string, payload any) bool {
	status := h.callCatalogStatus(c, method, path, payload)
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}

func (h *Handler) callCatalogStatus(c *gin.Context, method, path string, payload any) int {
	if h.catalogAPI == nil {
		return 0
	}
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return 0
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req = req.WithContext(c.Request.Context())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.catalogAPI.ServeHTTP(rec, req)
	return rec.Code
}

func (h *Handler) createProvider(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	enabled := true
	payload := map[string]any{"id": c.PostForm("id"), "name": c.PostForm("name"), "type": c.PostForm("type"), "kind": c.PostForm("kind"), "base_url": c.PostForm("base_url"), "api_key_env": c.PostForm("api_key_env"), "enabled": enabled}
	if !h.callCatalog(c, http.MethodPost, "/api/v1/providers", payload) {
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=provider")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/providers?message=provider-saved")
}
func (h *Handler) updateProvider(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	payload := map[string]any{"name": c.PostForm("name"), "type": c.PostForm("type"), "kind": c.PostForm("kind"), "base_url": c.PostForm("base_url"), "api_key_env": c.PostForm("api_key_env")}
	path := "/api/v1/providers/" + c.Param("id")
	if !h.callCatalog(c, http.MethodPut, path, payload) {
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=provider")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/providers?message=provider-saved")
}
func (h *Handler) updateModel(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	contextWindow, err1 := strconv.Atoi(c.PostForm("context_window"))
	maxOutput, err2 := strconv.Atoi(c.PostForm("max_output_tokens"))
	if err1 != nil || err2 != nil || contextWindow < 0 || maxOutput < 0 {
		c.Redirect(http.StatusSeeOther, "/dashboard/models?error=model")
		return
	}
	payload := map[string]any{"provider_id": c.PostForm("provider_id"), "name": c.PostForm("name"), "display_name": c.PostForm("display_name"), "context_window": contextWindow, "max_output_tokens": maxOutput, "supports_streaming": c.PostForm("supports_streaming") == "true"}
	path := "/api/v1/models/" + c.Param("id")
	if !h.callCatalog(c, http.MethodPut, path, payload) {
		c.Redirect(http.StatusSeeOther, "/dashboard/models?error=model")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/models?message=model-saved")
}

func (h *Handler) setProviderEnabled(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	enabled := c.PostForm("enabled") == "true"
	if !h.callCatalog(c, http.MethodPatch, "/api/v1/providers/"+c.Param("id")+"/enabled", map[string]any{"enabled": enabled}) {
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=provider")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/providers?message=provider-saved")
}
func (h *Handler) deleteProvider(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if !h.callCatalog(c, http.MethodDelete, "/api/v1/providers/"+c.Param("id"), nil) {
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=provider")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/providers?message=provider-deleted")
}
func (h *Handler) testProvider(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	status := h.callCatalogStatus(c, http.MethodPost, "/api/v1/providers/"+c.Param("id")+"/test", map[string]any{})
	switch {
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?message=provider-tested")
	case status == http.StatusConflict:
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=provider-test-model")
	case status == http.StatusGatewayTimeout:
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=provider-test-timeout")
	default:
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=provider-test-failed")
	}
}
func (h *Handler) createModel(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	contextWindow, err1 := strconv.Atoi(c.PostForm("context_window"))
	maxOutput, err2 := strconv.Atoi(c.PostForm("max_output_tokens"))
	if err1 != nil || err2 != nil || contextWindow < 0 || maxOutput < 0 {
		c.Redirect(http.StatusSeeOther, "/dashboard/models?error=model")
		return
	}
	payload := map[string]any{"id": c.PostForm("id"), "provider_id": c.PostForm("provider_id"), "name": c.PostForm("name"), "display_name": c.PostForm("display_name"), "context_window": contextWindow, "max_output_tokens": maxOutput, "supports_streaming": c.PostForm("supports_streaming") == "true", "enabled": true}
	if !h.callCatalog(c, http.MethodPost, "/api/v1/models", payload) {
		c.Redirect(http.StatusSeeOther, "/dashboard/models?error=model")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/models?message=model-saved")
}
func (h *Handler) setModelEnabled(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	enabled := c.PostForm("enabled") == "true"
	if !h.callCatalog(c, http.MethodPatch, "/api/v1/models/"+c.Param("id")+"/enabled", map[string]any{"enabled": enabled}) {
		c.Redirect(http.StatusSeeOther, "/dashboard/models?error=model")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/models?message=model-saved")
}
func (h *Handler) deleteModel(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if !h.callCatalog(c, http.MethodDelete, "/api/v1/models/"+c.Param("id"), nil) {
		c.Redirect(http.StatusSeeOther, "/dashboard/models?error=model")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/models?message=model-deleted")
}

func (h *Handler) providers(c *gin.Context) {
	data := pageData{Title: "Providers", Active: "providers", ProviderRows: []providerView{}}
	if h.providerRegistry != nil {
		for _, p := range h.providerRegistry.List() {
			row := providerView{ID: string(p.ID), Name: p.Name, Type: string(p.Type), Kind: string(p.Kind), BaseURL: p.BaseURL, Status: "disabled"}
			if p.Enabled {
				row.Status = "enabled"
			}
			if h.credentials != nil {
				record, err := h.credentials.Status(c.Request.Context(), p.ID)
				if err == nil {
					row.CredentialStatus = string(record.Status)
				} else {
					row.CredentialStatus = "unavailable"
				}
			} else {
				row.CredentialStatus = "unavailable"
			}
			data.ProviderRows = append(data.ProviderRows, row)
		}
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) setCredential(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.credentials == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	secret, err := domaincredential.NewSecret(c.PostForm("api_key"))
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=credential")
		return
	}
	if err = h.credentials.Set(c.Request.Context(), domainprovider.ID(c.Param("id")), secret); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=credential")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/providers?message=credential-saved")
}

func (h *Handler) deleteCredential(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.credentials == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if err := h.credentials.Delete(c.Request.Context(), domainprovider.ID(c.Param("id"))); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=credential")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/providers?message=credential-deleted")
}

func (h *Handler) models(c *gin.Context) {
	data := pageData{Title: "Models", Active: "models", ModelRows: []modelView{}}
	if h.providerRegistry != nil {
		for _, provider := range h.providerRegistry.List() {
			option := providerOption{ID: string(provider.ID), Name: provider.Name}
			data.AllProviderOptions = append(data.AllProviderOptions, option)
			if provider.Enabled {
				data.ProviderOptions = append(data.ProviderOptions, option)
			}
		}
	}
	if h.catalogAPI != nil {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil).WithContext(c.Request.Context())
		rec := httptest.NewRecorder()
		h.catalogAPI.ServeHTTP(rec, req)
		var response map[string]json.RawMessage
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
			data.Error = "Could not load model catalog"
		} else {
			var items []map[string]json.RawMessage
			if err := json.Unmarshal(response["data"], &items); err != nil {
				data.Error = "Could not decode model catalog"
			} else {
				for _, item := range items {
					var v modelView
					_ = json.Unmarshal(item["id"], &v.ID)
					_ = json.Unmarshal(item["provider_id"], &v.ProviderID)
					_ = json.Unmarshal(item["name"], &v.Name)
					_ = json.Unmarshal(item["display_name"], &v.DisplayName)
					_ = json.Unmarshal(item["status"], &v.Status)
					_ = json.Unmarshal(item["source"], &v.Source)
					_ = json.Unmarshal(item["context_window"], &v.ContextWindow)
					_ = json.Unmarshal(item["max_output_tokens"], &v.MaxOutputTokens)
					_ = json.Unmarshal(item["supports_streaming"], &v.Streaming)
					data.ModelRows = append(data.ModelRows, v)
				}
			}
		}
	} else if h.modelRegistry != nil {
		for _, m := range h.modelRegistry.List() {
			status := "disabled"
			if m.Enabled {
				status = "enabled"
			}
			data.ModelRows = append(data.ModelRows, modelView{ID: m.ID, ProviderID: string(m.ProviderID), Name: m.Name, DisplayName: m.DisplayName, Status: status, Source: "provider", ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxOutputTokens, Streaming: m.SupportsStreaming})
		}
	}
	h.render(c, data, http.StatusOK)
}

func parseToolList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' })
	tools := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := strings.TrimSpace(part); name != "" {
			tools = append(tools, name)
		}
	}
	return tools
}

func (h *Handler) createAgent(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.agentCatalog == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	temperature, err := strconv.ParseFloat(c.PostForm("temperature"), 64)
	if err != nil || math.IsNaN(temperature) || math.IsInf(temperature, 0) || temperature < 0 || temperature > 2 {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent")
		return
	}
	maxSteps, err1 := strconv.Atoi(c.PostForm("max_steps"))
	maxCalls, err2 := strconv.Atoi(c.PostForm("max_tool_calls"))
	maxTokens, err3 := strconv.Atoi(c.PostForm("max_output_tokens"))
	timeout, err4 := strconv.Atoi(c.PostForm("timeout_seconds"))
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent")
		return
	}
	d := domainagent.Definition{ID: c.PostForm("id"), Name: c.PostForm("name"), Description: c.PostForm("description"), SystemPrompt: c.PostForm("system_prompt"), ModelID: c.PostForm("model_id"), Temperature: temperature, MaxSteps: maxSteps, MaxToolCalls: maxCalls, MaxOutputTokens: maxTokens, TimeoutSeconds: timeout, ApprovedTools: parseToolList(c.PostForm("approved_tools")), Enabled: true}
	if err = h.agentCatalog.Create(c.Request.Context(), d); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/agents?message=agent-saved")
}
func (h *Handler) runAgent(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.agentCatalog == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	workspaceID, prompt := c.PostForm("workspace_id"), c.PostForm("prompt")
	if workspaceID == "" || prompt == "" {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent-run")
		return
	}
	if h.workspaces == nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=workspace-unavailable")
		return
	}
	if _, err := h.workspaces.Ensure(c.Request.Context(), workspaceID); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=workspace-unavailable")
		return
	}
	result, err := h.agentCatalog.Run(c.Request.Context(), c.Param("id"), workspaceID, prompt)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent-run")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/agents?message=agent-run-"+string(result.Status))
}
func (h *Handler) runWorkflow(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.workflowEngine == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	input := map[string]any{}
	if raw := c.PostForm("input_json"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow-run")
			return
		}
	}
	maxAttempts := h.workflowMaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 3
	}
	run, err := h.workflowEngine.StartQueued(c.Request.Context(), c.Param("id"), "", input, maxAttempts)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow-run")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/workflow-runs/"+url.PathEscape(run.ID))
}

func (h *Handler) updateAgent(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.agentCatalog == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	d, err := h.agentCatalog.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent")
		return
	}
	if value := c.PostForm("name"); value != "" {
		d.Name = value
	}
	if value := c.PostForm("description"); value != "" {
		d.Description = value
	}
	if value := c.PostForm("model_id"); value != "" {
		d.ModelID = value
	}
	if value := c.PostForm("system_prompt"); value != "" {
		d.SystemPrompt = value
	}
	if value := c.PostForm("approved_tools"); value != "" {
		d.ApprovedTools = parseToolList(value)
	}
	if value := c.PostForm("temperature"); value != "" {
		d.Temperature, err = strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(d.Temperature) || math.IsInf(d.Temperature, 0) || d.Temperature < 0 || d.Temperature > 2 {
			c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent")
			return
		}
	}
	for name, target := range map[string]*int{"max_steps": &d.MaxSteps, "max_tool_calls": &d.MaxToolCalls, "max_output_tokens": &d.MaxOutputTokens, "timeout_seconds": &d.TimeoutSeconds} {
		if value := c.PostForm(name); value != "" {
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent")
				return
			}
			*target = parsed
		}
	}
	if err = h.agentCatalog.Update(c.Request.Context(), d); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/agents?message=agent-saved")
}

func (h *Handler) setAgentEnabled(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.agentCatalog == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	d, err := h.agentCatalog.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent")
		return
	}
	d.Enabled = c.PostForm("enabled") == "true"
	if err = h.agentCatalog.Update(c.Request.Context(), d); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/agents?message=agent-saved")
}
func (h *Handler) deleteAgent(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.agentCatalog == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if err := h.agentCatalog.Delete(c.Request.Context(), c.Param("id")); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=agent")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/agents?message=agent-deleted")
}
func (h *Handler) createQuickstartWorkspace(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.collaboration == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}

	name := strings.TrimSpace(c.PostForm("name"))
	slug := strings.ToLower(strings.TrimSpace(c.PostForm("slug")))
	description := strings.TrimSpace(c.PostForm("description"))
	if name == "" || len(name) > 160 || !validWorkspaceSlug(slug) {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workspace-create")
		return
	}
	workspace := collaboration.Workspace{
		Name: name, Slug: slug, Description: description,
		AutonomyMode: "hybrid", MaxAgentConcurrency: 4, MaxDelegationDepth: 3,
	}
	if h.workspaces == nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workspace-create")
		return
	}
	created, err := h.collaboration.CreateWorkspace(c.Request.Context(), workspace)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workspace-create")
		return
	}
	if _, err := h.workspaces.Ensure(c.Request.Context(), created.ID); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workspace-create")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/workflows?message=workspace-created")
}

func (h *Handler) createQuickstartWorkflowTemplate(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.workflowRepo == nil || h.modelRegistry == nil || h.collaboration == nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow-quickstart")
		return
	}

	name := strings.TrimSpace(c.PostForm("name"))
	modelID := strings.TrimSpace(c.PostForm("model_id"))
	workspaceID := strings.TrimSpace(c.PostForm("workspace_id"))
	prompt := strings.TrimSpace(c.PostForm("prompt"))
	if name == "" || len(name) > 160 || modelID == "" || workspaceID == "" || prompt == "" || len(prompt) > 10000 {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow-quickstart")
		return
	}

	modelEnabled := false
	for _, model := range h.modelRegistry.List() {
		if model.ID == modelID && model.Enabled {
			modelEnabled = true
			break
		}
	}
	if !modelEnabled {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow-quickstart-model")
		return
	}
	if _, err := h.collaboration.GetWorkspace(c.Request.Context(), workspaceID); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow-quickstart-workspace")
		return
	}
	if h.workspaces == nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow-quickstart-workspace")
		return
	}
	if _, err := h.workspaces.Ensure(c.Request.Context(), workspaceID); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow-quickstart-workspace")
		return
	}

	id := "quickstart-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	definition := domainworkflow.Definition{
		ID: id, Name: name, Version: 1, Entry: "start",
		Nodes: []domainworkflow.Node{
			{ID: "start", Type: domainworkflow.NodeStart},
			{ID: "agent", Type: domainworkflow.NodeAgent, Name: "Run Agent task", Agent: &domainworkflow.AgentConfig{
				ModelID: modelID, WorkspaceID: workspaceID, Prompt: prompt, MaxSteps: 10, MaxToolCalls: 10,
			}},
			{ID: "end", Type: domainworkflow.NodeEnd},
		},
		Edges: []domainworkflow.Edge{{From: "start", To: "agent"}, {From: "agent", To: "end"}},
	}
	template := authoring.Template{ID: id, Name: name, Description: "Starter Workflow created from the guided quickstart.", Definition: definition}
	if err := h.workflowRepo.SaveTemplate(c.Request.Context(), template); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow-quickstart")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/workflows?message=workflow-quickstart-saved")
}

func (h *Handler) createWorkflowTemplate(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.workflowRepo == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	var definition domainworkflow.Definition
	if err := json.Unmarshal([]byte(c.PostForm("definition_json")), &definition); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow")
		return
	}
	if name := c.PostForm("name"); name != "" {
		definition.Name = name
	}
	t := authoring.Template{ID: definition.ID, Name: definition.Name, Description: c.PostForm("description"), Definition: definition}
	if err := h.workflowRepo.SaveTemplate(c.Request.Context(), t); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/workflows?message=workflow-saved")
}
func (h *Handler) applyWorkflowTemplate(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.workflowRepo == nil || h.workflowEngine == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	t, err := h.workflowRepo.GetTemplate(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow")
		return
	}
	workflowID := c.PostForm("workflow_id")
	if workflowID == "" {
		workflowID = t.ID
	}
	version, err := strconv.Atoi(c.PostForm("version"))
	if err != nil || version < 1 {
		version = t.Definition.Version
		if version < 1 {
			version = 1
		}
	}
	definition := t.Definition
	definition.ID = workflowID
	definition.Version = version
	if err = h.workflowEngine.PutDefinition(c.Request.Context(), definition); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/workflows?message=workflow-applied")
}
func (h *Handler) deleteWorkflowTemplate(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.workflowRepo == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if err := h.workflowRepo.DeleteTemplate(c.Request.Context(), c.Param("id")); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/workflows?error=workflow")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/workflows?message=workflow-deleted")
}

func (h *Handler) createAgentIdentity(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.collaboration == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	identity := collaboration.AgentIdentity{WorkspaceID: c.PostForm("workspace_id"), DefinitionID: c.PostForm("definition_id"), Name: c.PostForm("name"), Description: c.PostForm("description"), Role: c.PostForm("role"), Capabilities: parseToolList(c.PostForm("capabilities"))}
	if _, err := h.collaboration.CreateAgent(c.Request.Context(), identity); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=identity")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/agents?message=identity-saved")
}
func (h *Handler) enableAgentIdentity(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.collaboration == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if _, err := h.collaboration.EnableAgent(c.Request.Context(), c.Param("id")); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=identity")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/agents?message=identity-saved")
}
func (h *Handler) disableAgentIdentity(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.collaboration == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if _, err := h.collaboration.DisableAgent(c.Request.Context(), c.Param("id")); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=identity")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/agents?message=identity-saved")
}
func (h *Handler) deleteAgentIdentity(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.collaboration == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if _, err := h.collaboration.DeleteAgent(c.Request.Context(), c.Param("id")); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/agents?error=identity")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/agents?message=identity-deleted")
}

func (h *Handler) agents(c *gin.Context) {
	data := pageData{Title: "Agents", Active: "agents", AgentRows: []agentView{}}
	if h.modelRegistry != nil {
		for _, model := range h.modelRegistry.List() {
			name := model.DisplayName
			if name == "" {
				name = model.Name
			}
			option := modelOption{ID: model.ID, Name: name}
			data.AllModelOptions = append(data.AllModelOptions, option)
			if model.Enabled {
				data.ModelOptions = append(data.ModelOptions, option)
			}
		}
	}
	if h.collaboration != nil {
		workspaces, err := h.collaboration.ListWorkspaces(c.Request.Context())
		if err == nil {
			for _, workspace := range workspaces {
				data.WorkspaceOptions = append(data.WorkspaceOptions, workspaceOption{ID: workspace.ID, Name: workspace.Name})
				identities, listErr := h.collaboration.ListAgents(c.Request.Context(), workspace.ID)
				if listErr != nil {
					continue
				}
				for _, identity := range identities {
					data.IdentityRows = append(data.IdentityRows, identityView{ID: identity.ID, WorkspaceID: identity.WorkspaceID, DefinitionID: identity.DefinitionID, Name: identity.Name, Description: identity.Description, Role: identity.Role, Status: identity.Status, Capabilities: strings.Join(identity.Capabilities, ", ")})
				}
			}
		}
	}
	if h.agentCatalog != nil {
		items, err := h.agentCatalog.List(c.Request.Context())
		if err != nil {
			data.Error = "Could not load agent definitions"
		} else {
			for _, item := range items {
				runs, _ := h.agentCatalog.ListRuns(c.Request.Context(), item.ID, 5)
				runRows := make([]agentRunView, 0, len(runs))
				for _, run := range runs {
					row := agentRunView{ID: run.ID, TaskID: run.TaskID, Status: run.Status, CreatedAt: run.CreatedAt.UTC().Format(time.RFC3339)}
					row.ExecutionID = h.executionForTask(c.Request.Context(), run.TaskID)
					runRows = append(runRows, row)
				}
				data.AgentRows = append(data.AgentRows, agentView{ID: item.ID, Name: item.Name, ModelID: item.ModelID, Description: item.Description, SystemPrompt: item.SystemPrompt, ApprovedTools: strings.Join(item.ApprovedTools, ", "), Temperature: item.Temperature, MaxSteps: item.MaxSteps, MaxToolCalls: item.MaxToolCalls, MaxOutputTokens: item.MaxOutputTokens, TimeoutSeconds: item.TimeoutSeconds, Runs: runRows, Enabled: item.Enabled, Version: item.Version})
			}
		}
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) executionForTask(ctx context.Context, taskID string) string {
	if h.db == nil || taskID == "" {
		return ""
	}
	var id string
	if err := h.db.QueryRowContext(ctx, "SELECT id FROM executions WHERE kind='agent' AND task_id=? ORDER BY created_at DESC LIMIT 1", taskID).Scan(&id); err != nil {
		return ""
	}
	return id
}

func (h *Handler) executionForRuntimeRef(ctx context.Context, kind, runtimeRef string) string {
	if h.db == nil || runtimeRef == "" {
		return ""
	}
	var id string
	if err := h.db.QueryRowContext(ctx, "SELECT id FROM executions WHERE kind=? AND runtime_ref=? ORDER BY created_at DESC LIMIT 1", kind, runtimeRef).Scan(&id); err != nil {
		return ""
	}
	return id
}

func (h *Handler) tasks(c *gin.Context) {
	data := pageData{Title: "Tasks", Active: "tasks", TaskRows: []taskView{}}
	if h.collaboration != nil {
		workspaces, err := h.collaboration.ListWorkspaces(c.Request.Context())
		if err != nil {
			data.Error = "Could not load workspaces"
		} else {
			for _, workspace := range workspaces {
				data.WorkspaceOptions = append(data.WorkspaceOptions, workspaceOption{ID: workspace.ID, Name: workspace.Name})
			}
			for _, workspace := range workspaces {
				tasks, listErr := h.collaboration.ListTasks(c.Request.Context(), workspace.ID)
				if listErr != nil {
					data.Error = "Could not load workspace tasks"
					continue
				}
				identities, _ := h.collaboration.ListAgents(c.Request.Context(), workspace.ID)
				agentOptions := make([]agentOption, 0, len(identities))
				for _, identity := range identities {
					agentOptions = append(agentOptions, agentOption{ID: identity.ID, Name: identity.Name, Role: identity.Role, Status: identity.Status, Capabilities: strings.Join(identity.Capabilities, ", ")})
				}
				for _, task := range tasks {
					row := taskView{TaskID: task.ID, WorkspaceID: task.WorkspaceID, ParentTaskID: task.ParentTaskID, Title: task.Title, Type: task.Type, Status: task.Status, Priority: task.Priority, CreatedAt: task.CreatedAt.UTC().Format(time.RFC3339), AgentOptions: agentOptions}
					row.ExecutionID = h.executionForAnyTask(c.Request.Context(), task.ID)
					data.TaskRows = append(data.TaskRows, row)
				}
			}
			sort.Slice(data.TaskRows, func(i, j int) bool { return data.TaskRows[i].CreatedAt > data.TaskRows[j].CreatedAt })
			if len(data.TaskRows) > 100 {
				data.TaskRows = data.TaskRows[:100]
			}
		}
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) createTask(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.collaboration == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	priority, err := strconv.Atoi(c.PostForm("priority"))
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/tasks?error=task")
		return
	}
	task := collaboration.Task{
		WorkspaceID: c.PostForm("workspace_id"), ParentTaskID: c.PostForm("parent_task_id"),
		Title: c.PostForm("title"), Description: c.PostForm("description"), Type: c.PostForm("type"),
		Priority: priority, CreatedBy: "dashboard", IdempotencyKey: c.PostForm("idempotency_key"),
		RequiredCapabilities: parseToolList(c.PostForm("required_capabilities")),
	}
	if _, err = h.collaboration.CreateTask(c.Request.Context(), task); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/tasks?error=task")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/tasks?message=task-saved")
}

func (h *Handler) assignTask(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.collaboration == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	agentID := c.PostForm("agent_id")
	if agentID == "" {
		c.Redirect(http.StatusSeeOther, "/dashboard/tasks?error=task-assign")
		return
	}
	if _, err := h.collaboration.AssignTask(c.Request.Context(), c.Param("id"), agentID, "dashboard"); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/tasks?error=task-assign")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/tasks?message=task-assigned")
}

func (h *Handler) executionForAnyTask(ctx context.Context, taskID string) string {
	if h.db == nil || taskID == "" {
		return ""
	}
	var id string
	if err := h.db.QueryRowContext(ctx, "SELECT id FROM executions WHERE task_id=? ORDER BY created_at DESC LIMIT 1", taskID).Scan(&id); err != nil {
		return ""
	}
	return id
}

func (h *Handler) workflows(c *gin.Context) {
	data := pageData{Title: "Workflows", Active: "workflows", WorkflowRows: []workflowView{}, WorkflowRuns: []workflowRunListView{}}
	if h.modelRegistry != nil {
		for _, model := range h.modelRegistry.List() {
			if !model.Enabled {
				continue
			}
			name := model.DisplayName
			if name == "" {
				name = model.Name
			}
			data.ModelOptions = append(data.ModelOptions, modelOption{ID: model.ID, Name: name})
		}
	}
	if h.collaboration != nil {
		workspaces, err := h.collaboration.ListWorkspaces(c.Request.Context())
		if err == nil {
			for _, workspace := range workspaces {
				data.WorkspaceOptions = append(data.WorkspaceOptions, workspaceOption{ID: workspace.ID, Name: workspace.Name})
			}
		} else {
			data.Error = "Could not load workspaces for the Workflow quickstart"
		}
	}
	if h.db != nil {
		rows, err := h.db.QueryContext(c.Request.Context(), `SELECT r.id,r.workflow_id,r.workflow_version,r.status,r.created_at,r.updated_at,COALESCE(w.name,r.workflow_id)
			FROM workflow_runs r LEFT JOIN workflows w ON w.id=r.workflow_id AND w.version=r.workflow_version
			ORDER BY r.created_at DESC LIMIT 100`)
		if err != nil {
			data.Error = "Could not load workflow runs"
		} else {
			for rows.Next() {
				var item workflowRunListView
				var created, updated int64
				if err = rows.Scan(&item.ID, &item.WorkflowID, &item.Version, &item.Status, &created, &updated, &item.WorkflowName); err != nil {
					data.Error = "Could not read workflow runs"
					break
				}
				item.CreatedAt = time.Unix(created, 0).UTC().Format(time.RFC3339)
				item.UpdatedAt = time.Unix(updated, 0).UTC().Format(time.RFC3339)
				data.WorkflowRuns = append(data.WorkflowRuns, item)
			}
			if rowsErr := rows.Err(); rowsErr != nil {
				data.Error = "Could not read workflow runs"
			}
			_ = rows.Close()
		}
	}
	if h.workflowRepo != nil {
		items, err := h.workflowRepo.ListTemplates(c.Request.Context())
		if err != nil {
			data.Error = "Could not load workflow templates"
		} else {
			for _, item := range items {
				runs := []workflowRunView{}
				if h.workflowEngine != nil {
					workflowRuns, _ := h.workflowEngine.ListRuns(c.Request.Context(), item.ID, 5)
					for _, run := range workflowRuns {
						row := workflowRunView{ID: run.ID, Status: string(run.Status), Version: run.WorkflowVersion, CreatedAt: run.CreatedAt.UTC().Format(time.RFC3339)}
						row.ExecutionID = h.executionForRuntimeRef(c.Request.Context(), "workflow", run.ID)
						runs = append(runs, row)
					}
				}
				data.WorkflowRows = append(data.WorkflowRows, workflowView{ID: item.ID, Name: item.Name, Description: item.Description, Version: item.Definition.Version, UpdatedAt: item.UpdatedAt.Format(time.RFC3339), Runs: runs})
			}
		}
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) workflowRun(c *gin.Context) {
	if h.workflowEngine == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	ctx := c.Request.Context()
	run, err := h.workflowEngine.GetRun(ctx, c.Param("id"))
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	definition, err := h.workflowEngine.Definition(ctx, run.WorkflowID, run.WorkflowVersion)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	detail := &workflowRunDetailView{
		ID: run.ID, WorkflowID: run.WorkflowID, WorkflowName: definition.Name,
		Status: string(run.Status), CurrentNode: run.CurrentNode, Version: run.WorkflowVersion,
		CreatedAt: run.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: run.UpdatedAt.UTC().Format(time.RFC3339),
		Nodes:     []workflowNodeView{}, Events: []workflowEventView{},
	}
	if !run.FinishedAt.IsZero() {
		detail.FinishedAt = run.FinishedAt.UTC().Format(time.RFC3339)
	}
	observations, err := h.workflowEngine.NodeObservations(ctx, run.ID)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	for _, node := range observations {
		view := workflowNodeView{ID: node.NodeID, Status: string(node.Status), Attempt: node.Attempt, DurationMillis: node.DurationMillis}
		if !node.StartedAt.IsZero() {
			view.StartedAt = node.StartedAt.UTC().Format(time.RFC3339)
		}
		if !node.FinishedAt.IsZero() {
			view.FinishedAt = node.FinishedAt.UTC().Format(time.RFC3339)
		}
		detail.Nodes = append(detail.Nodes, view)
	}
	events, err := h.workflowEngine.Events(ctx, run.ID, 0, 100)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	for _, event := range events {
		view := workflowEventView{Sequence: event.Sequence, Type: event.Type, NodeID: event.NodeID, OccurredAt: event.OccurredAt.UTC().Format(time.RFC3339Nano)}
		detail.Events = append(detail.Events, view)
	}
	h.render(c, pageData{Title: "Workflow Run " + run.ID, Active: "workflows", WorkflowRun: detail}, http.StatusOK)
}

func (h *Handler) codingTasks(c *gin.Context) {
	data := pageData{Title: "Coding", Active: "coding", CodingRows: []coding.Task{}, CodingSandboxImage: h.sandboxImage, CodingSandboxNetworkEnabled: h.sandboxNetworkEnabled}
	if c.Query("error") == "create" {
		data.Error = "Could not create the Coding Task. Check the repository allowlist, model ID, prompt, test command and task limits."
	}
	if h.codingService != nil {
		items, err := h.codingService.List(c.Request.Context(), 100)
		if err != nil {
			data.Error = "Could not load coding tasks"
		} else {
			data.CodingRows = items
		}
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) createCodingTask(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.manager == nil || h.codingService == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	maxRepair, _ := strconv.Atoi(c.DefaultPostForm("max_repair_attempts", "2"))
	timeout, _ := strconv.Atoi(c.DefaultPostForm("timeout_seconds", "1800"))
	request := coding.CreateRequest{
		ID:                fmt.Sprintf("dashboard-%d", time.Now().UTC().UnixNano()),
		Title:             strings.TrimSpace(c.PostForm("title")),
		RepositoryPath:    strings.TrimSpace(c.PostForm("repository_path")),
		ModelID:           strings.TrimSpace(c.PostForm("model_id")),
		Prompt:            strings.TrimSpace(c.PostForm("prompt")),
		TestCommand:       coding.Command{Program: strings.TrimSpace(c.PostForm("test_program")), Args: strings.Fields(c.PostForm("test_args"))},
		MaxRepairAttempts: maxRepair, TimeoutSeconds: timeout,
	}
	if err := request.Validate(); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/coding?error=create")
		return
	}
	input, err := json.Marshal(request)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/coding?error=create")
		return
	}
	_, err = h.manager.Submit(c.Request.Context(), manager.SubmitRequest{
		Kind: execdom.KindCoding, Runtime: execdom.RuntimeCoding, TaskID: request.ID,
		Input: input, IdempotencyKey: "dashboard-coding:" + request.ID,
	})
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/coding?error=create")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(request.ID)+"?message=coding-queued")
}

func (h *Handler) codingTask(c *gin.Context) {
	if h.codingService == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	task, err := h.codingService.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	data := pageData{Title: "Coding " + task.ID, Active: "coding", CodingTask: &task, CodingSandboxImage: h.sandboxImage, CodingSandboxNetworkEnabled: h.sandboxNetworkEnabled}
	switch c.Query("message") {
	case "coding-queued":
		data.Message = "Coding task submitted. Review the Worktree diff and evidence before keeping changes."
	case "cancel-requested":
		data.Message = "Cancellation requested."
	case "reverted":
		data.Message = "Worktree reset to its base commit."
	case "cleaned":
		data.Message = "Worktree and task branch cleaned up."
	case "resolved-keep":
		data.Message = "Outcome manually resolved. Changes are retained in the Worktree; the Execution is not marked successful."
	case "resolved-discard":
		data.Message = "Outcome manually resolved. Worktree was reset to its base commit."
	}
	if c.Query("error") != "" {
		data.Error = "The requested Coding operation could not be completed. Check the current task status and event history."
	}
	data.CodingSteps, err = h.codingService.Steps(c.Request.Context(), task.ID)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	data.CodingEvents, err = h.codingService.Events(c.Request.Context(), task.ID)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	data.CodingSnapshots, err = h.codingService.Snapshots(c.Request.Context(), task.ID)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if task.WorkspacePath != "" && task.Status != coding.StatusRunning && task.Status != coding.StatusTesting && task.Status != coding.StatusRepairing {
		data.CodingDiff, _ = h.codingService.Diff(c.Request.Context(), task.ID)
	}
	data.Execution = nil
	h.render(c, data, http.StatusOK)
}

func (h *Handler) cancelCodingTask(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.codingService == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if err := h.codingService.CancelContext(c.Request.Context(), c.Param("id")); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?error=cancel")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?message=cancel-requested")
}

func (h *Handler) resolveCodingTask(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.codingService == nil || h.manager == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if c.PostForm("confirm") != "yes" {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	decision := strings.TrimSpace(c.PostForm("decision"))
	reason := strings.TrimSpace(c.PostForm("reason"))
	before, err := h.codingService.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?error=resolve")
		return
	}
	if before.ExecutionID != "" {
		// Observe the interrupted child state before applying the operator's
		// terminal decision to the canonical Execution.
		_, _ = h.manager.Get(c.Request.Context(), before.ExecutionID)
	}
	task, err := h.codingService.ResolveInterrupted(c.Request.Context(), c.Param("id"), decision, reason)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?error=resolve")
		return
	}
	if task.ExecutionID != "" {
		status := execdom.LifecycleFailed
		if decision == "discard" {
			status = execdom.LifecycleCanceled
		}
		result, marshalErr := json.Marshal(task)
		if marshalErr != nil {
			c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?error=resolve")
			return
		}
		if _, err = h.manager.Resolve(c.Request.Context(), task.ExecutionID, status, result,
			"coding outcome manually reviewed; decision="+decision+"; reason="+reason); err != nil {
			c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?error=resolve-execution")
			return
		}
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?message=resolved-"+decision)
}

func (h *Handler) revertCodingTask(c *gin.Context) {
	if !sameOrigin(c.Request) || c.PostForm("confirm") != "yes" {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.codingService == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if err := h.codingService.Revert(c.Request.Context(), c.Param("id")); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?error=revert")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?message=reverted")
}

func (h *Handler) cleanupCodingTask(c *gin.Context) {
	if !sameOrigin(c.Request) || c.PostForm("confirm") != "yes" {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.codingService == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if err := h.codingService.Cleanup(c.Request.Context(), c.Param("id")); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?error=cleanup")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/coding/"+url.PathEscape(c.Param("id"))+"?message=cleaned")
}

func (h *Handler) executions(c *gin.Context) {
	data := pageData{
		Title: "Executions", Active: "executions", Executions: []executionRow{},
		FilterStatus: c.Query("status"), FilterKind: c.Query("kind"), FilterRuntime: c.Query("runtime"), FilterTask: c.Query("task"),
	}
	if h.db == nil {
		h.render(c, data, http.StatusOK)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}
	data.Offset = offset
	data.Limit = limit
	data.PrevOffset = offset - limit
	if data.PrevOffset < 0 {
		data.PrevOffset = 0
	}
	data.NextOffset = offset + limit
	data.HasPrevious = offset > 0

	where := []string{"1=1"}
	args := []any{}
	if data.FilterStatus != "" {
		where = append(where, "status=?")
		args = append(args, data.FilterStatus)
	}
	if data.FilterKind != "" {
		where = append(where, "kind=?")
		args = append(args, data.FilterKind)
	}
	if data.FilterRuntime != "" {
		where = append(where, "runtime=?")
		args = append(args, data.FilterRuntime)
	}
	if data.FilterTask != "" {
		where = append(where, "task_id=?")
		args = append(args, data.FilterTask)
	}
	query := `SELECT id,kind,runtime,COALESCE(task_id,''),status,created_at,updated_at FROM executions WHERE ` + strings.Join(where, " AND ") + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit+1, offset)
	rows, err := h.db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		data.Error = "Could not load executions"
		h.render(c, data, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var item executionRow
		var created, updated int64
		if err = rows.Scan(&item.ID, &item.Kind, &item.Runtime, &item.TaskID, &item.Status, &created, &updated); err != nil {
			data.Error = "Could not read executions"
			h.render(c, data, http.StatusInternalServerError)
			return
		}
		item.CreatedAt = time.Unix(0, created).UTC().Format(time.RFC3339)
		item.UpdatedAt = time.Unix(0, updated).UTC().Format(time.RFC3339)
		data.Executions = append(data.Executions, item)
	}
	if rows.Err() != nil {
		data.Error = "Could not read executions"
		h.render(c, data, http.StatusInternalServerError)
		return
	}
	if len(data.Executions) > limit {
		data.Executions = data.Executions[:limit]
		data.HasNext = true
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) execution(c *gin.Context) {
	if h.db == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	var item executionDetail
	var created, updated int64
	query := `SELECT id,kind,runtime,COALESCE(task_id,''),status,created_at,updated_at
		FROM executions WHERE id=?`
	err := h.db.QueryRowContext(c.Request.Context(), query, c.Param("id")).Scan(
		&item.ID, &item.Kind, &item.Runtime, &item.TaskID, &item.Status, &created, &updated,
	)
	if err == sql.ErrNoRows {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	item.CreatedAt = time.Unix(0, created).UTC().Format(time.RFC3339)
	item.UpdatedAt = time.Unix(0, updated).UTC().Format(time.RFC3339)
	data := pageData{Title: "Execution " + item.ID, Active: "executions", Execution: &item}
	if err := h.loadExecutionRelations(c, item.ID, &data); err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) loadExecutionRelations(c *gin.Context, id string, data *pageData) error {
	data.Attempts = []attemptView{}
	rows, err := h.db.QueryContext(c.Request.Context(), `SELECT id,attempt_number,runtime,status,started_at,finished_at FROM execution_attempts WHERE execution_id=? ORDER BY attempt_number`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v attemptView
		var started int64
		var finished sql.NullInt64
		if err = rows.Scan(&v.ID, &v.Number, &v.Runtime, &v.Status, &started, &finished); err != nil {
			rows.Close()
			return err
		}
		v.StartedAt = time.Unix(0, started).UTC().Format(time.RFC3339)
		if finished.Valid {
			v.FinishedAt = time.Unix(0, finished.Int64).UTC().Format(time.RFC3339)
		}
		data.Attempts = append(data.Attempts, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}

	data.Artifacts = []artifactView{}
	rows, err = h.db.QueryContext(c.Request.Context(), `SELECT id,artifact_id,version,COALESCE(media_type,''),COALESCE(content_sha256,''),COALESCE(size_bytes,0),COALESCE(source_execution_id,''),COALESCE(source_attempt_id,'') FROM collaboration_artifact_versions WHERE source_execution_id=? OR source_attempt_id IN (SELECT id FROM execution_attempts WHERE execution_id=?) ORDER BY artifact_id,version`, id, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v artifactView
		if err = rows.Scan(&v.ID, &v.ArtifactID, &v.Version, &v.MediaType, &v.Hash, &v.Size, &v.SourceExecutionID, &v.SourceAttemptID); err != nil {
			rows.Close()
			return err
		}
		data.Artifacts = append(data.Artifacts, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}

	data.Evidence = []evidenceView{}
	rows, err = h.db.QueryContext(c.Request.Context(), `SELECT id,artifact_id,version_id,kind,COALESCE(statement,''),COALESCE(execution_id,''),COALESCE(attempt_id,'') FROM collaboration_artifact_evidence WHERE execution_id=? OR attempt_id IN (SELECT id FROM execution_attempts WHERE execution_id=?) ORDER BY created_at,id`, id, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v evidenceView
		if err = rows.Scan(&v.ID, &v.ArtifactID, &v.VersionID, &v.Kind, &v.Statement, &v.ExecutionID, &v.AttemptID); err != nil {
			rows.Close()
			return err
		}
		data.Evidence = append(data.Evidence, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}

	data.Events = []eventView{}
	if h.executionAPI != nil {
		query := url.Values{}
		query.Set("limit", "50")
		if raw := c.Query("after_time_ns"); raw != "" {
			query.Set("after_time_ns", raw)
		}
		if raw := c.Query("after_stream"); raw != "" {
			query.Set("after_stream", raw)
		}
		if raw := c.Query("after_sequence"); raw != "" {
			query.Set("after_sequence", raw)
		}
		path := "/api/v1/managed-executions/" + url.PathEscape(id) + "/timeline?" + query.Encode()
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(c.Request.Context())
		rec := httptest.NewRecorder()
		h.executionAPI.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return errors.New("could not load execution timeline")
		}
		var response map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			return err
		}
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(response["items"], &items); err != nil {
			return err
		}
		for _, item := range items {
			var v eventView
			var occurred time.Time
			_ = json.Unmarshal(item["stream"], &v.Stream)
			_ = json.Unmarshal(item["type"], &v.Type)
			_ = json.Unmarshal(item["actor_type"], &v.Actor)
			_ = json.Unmarshal(item["source"], &v.Source)
			_ = json.Unmarshal(item["sequence"], &v.Sequence)
			_ = json.Unmarshal(item["occurred_at"], &occurred)
			v.OccurredAt = occurred.UTC().Format(time.RFC3339Nano)
			data.Events = append(data.Events, v)
		}
		_ = json.Unmarshal(response["has_more"], &data.TimelineHasMore)
		if data.TimelineHasMore {
			var cursor map[string]json.RawMessage
			if err := json.Unmarshal(response["next_cursor"], &cursor); err != nil {
				return err
			}
			var afterTime, afterSequence int64
			var afterStream string
			_ = json.Unmarshal(cursor["after_time_ns"], &afterTime)
			_ = json.Unmarshal(cursor["after_sequence"], &afterSequence)
			_ = json.Unmarshal(cursor["after_stream"], &afterStream)
			next := url.Values{}
			next.Set("after_time_ns", strconv.FormatInt(afterTime, 10))
			next.Set("after_sequence", strconv.FormatInt(afterSequence, 10))
			next.Set("after_stream", afterStream)
			data.TimelineNextURL = "/dashboard/executions/" + url.PathEscape(id) + "?" + next.Encode()
		}
	}
	return nil
}

func (h *Handler) cancelExecution(c *gin.Context) {
	if !sameOrigin(c.Request) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if h.manager == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if err := h.manager.Cancel(c.Request.Context(), c.Param("id")); err != nil {
		c.Redirect(http.StatusSeeOther, "/dashboard/executions/"+c.Param("id")+"?error=cancel")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/executions/"+c.Param("id")+"?message=cancel-requested")
}

func (h *Handler) render(c *gin.Context, data pageData, status int) {
	switch c.Query("error") {
	case "provider-test-model":
		data.Error = "The provider is disabled or has no enabled model. Enable the provider and add or enable one of its models before testing."
	case "provider-test-timeout":
		data.Error = "The connection test timed out. Check the provider URL and network access, then try again."
	case "provider-test-failed":
		data.Error = "The connection test failed. Check the API key, provider URL, and API model name."
	case "workflow-quickstart":
		data.Error = "Enter a Workflow name, choose an enabled model and workspace, and provide a task instruction."
	case "workflow-quickstart-model":
		data.Error = "The selected model is no longer enabled. Enable a model and try again."
	case "workflow-quickstart-workspace":
		data.Error = "The selected workspace could not be found. Choose an existing workspace and try again."
	case "new-task-input":
		data.Error = "Choose an Agent and workspace, then describe the task in 10,000 characters or fewer."
	case "new-task-agent":
		data.Error = "That Agent is unavailable or disabled. Choose an enabled Agent."
	case "new-task-workspace":
		data.Error = "That workspace is unavailable. Choose an existing workspace."
	case "new-task-run":
		data.Error = "The Agent could not start this task. Check its model and configuration, then try again."
	case "":
	default:
		data.Error = "The requested operation could not be completed. Check the submitted values and current object state."
	}
	switch c.Query("message") {
	case "coding-queued":
		data.Message = "Coding task queued. Review the Worktree diff and evidence before keeping changes."
	case "reverted":
		data.Message = "Coding Worktree reset to its base commit."
	case "cleaned":
		data.Message = "Coding Worktree and branch cleaned up."
	case "cancel-requested":
		data.Message = "Cancellation request recorded; the runtime outcome is not yet confirmed."
	case "credential-saved":
		data.Message = "Credential saved. The secret is write-only and will not be displayed."
	case "credential-deleted":
		data.Message = "Stored credential deleted."
	case "provider-saved":
		data.Message = "Provider configuration saved."
	case "provider-deleted":
		data.Message = "Provider deleted."
	case "workflow-quickstart-saved":
		data.Message = "Starter Workflow saved. Review it below, apply it to the Workflow Runtime, then queue a run."
	case "provider-tested":
		data.Message = "Provider connectivity test completed successfully."
	case "model-saved":
		data.Message = "Model configuration saved."
	case "model-deleted":
		data.Message = "Model deleted."
	case "agent-saved":
		data.Message = "Agent definition saved."
	case "agent-deleted":
		data.Message = "Agent definition deleted."
	case "agent-run-succeeded":
		data.Message = "Agent run completed successfully."
	case "agent-run-failed":
		data.Message = "Agent run failed. Review the Agent run history for details."
	case "agent-run-canceled":
		data.Message = "Agent run was canceled."
	case "agent-run-timed_out":
		data.Message = "Agent run timed out."
	case "workflow-saved":
		data.Message = "Workflow template saved."
	case "workflow-applied":
		data.Message = "Workflow template applied to the workflow engine."
	case "workflow-deleted":
		data.Message = "Workflow template deleted."
	case "workflow-run-queued":
		data.Message = "Workflow run queued."
	case "shutdown-requested":
		data.Message = "Graceful shutdown requested. The service will stop after in-flight work is drained."
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
	c.Header("Referrer-Policy", "same-origin")
	c.Status(status)
	if err := h.tmpl.ExecuteTemplate(c.Writer, "layout", data); err != nil {
		return
	}
}

func validWorkspaceSlug(slug string) bool {
	if slug == "" || len(slug) > 100 {
		return false
	}
	for i, r := range slug {
		isAlphaNumeric := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if !isAlphaNumeric && (r != '-' || i == 0 || i == len(slug)-1) {
			return false
		}
	}
	return true
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Referer()
	}
	if origin == "" {
		return false
	}
	u, err := http.NewRequest(http.MethodGet, origin, nil)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
