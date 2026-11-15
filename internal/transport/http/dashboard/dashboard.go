package dashboard

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/agentcatalog"
	"github.com/thkx/agentmesh/internal/collaboration"
	appcredential "github.com/thkx/agentmesh/internal/credential"
	domainagent "github.com/thkx/agentmesh/internal/domain/agent"
	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	domainprovider "github.com/thkx/agentmesh/internal/domain/provider"
	domainworkflow "github.com/thkx/agentmesh/internal/domain/workflow"
	"github.com/thkx/agentmesh/internal/execution/manager"
	"github.com/thkx/agentmesh/internal/transport/http/handlers"
	"github.com/thkx/agentmesh/internal/workflow/authoring"
	workflowruntime "github.com/thkx/agentmesh/internal/workflow/runtime"
)

//go:embed static/htmx.min.js
var htmxJS []byte

const sessionCookie = "agentmesh_dashboard"

type Dependencies struct {
	DB                  *sql.DB
	APIToken            string
	ExecutionManager    *manager.Manager
	Providers           *domainprovider.ProviderRegistry
	Models              *domainprovider.ModelRegistry
	Credentials         appcredential.Manager
	AgentDefinitions    *agentcatalog.Service
	WorkflowEngine      *workflowruntime.Engine
	WorkflowMaxAttempts int
	Catalog             *handlers.CatalogHandler
}

type Handler struct {
	db                  *sql.DB
	token               string
	manager             *manager.Manager
	providerRegistry    *domainprovider.ProviderRegistry
	modelRegistry       *domainprovider.ModelRegistry
	credentials         appcredential.Manager
	agentCatalog        *agentcatalog.Service
	collaboration       *collaboration.Service
	workflowEngine      *workflowruntime.Engine
	workflowMaxAttempts int
	workflowRepo        *authoring.Repository
	catalogAPI          http.Handler
	executionAPI        http.Handler
	tmpl                *template.Template
	loginMu             sync.Mutex
	loginAttempts       map[string][]time.Time
}

type workspaceOption struct { ID, Name string }

type pageData struct {
	Title           string
	Active          string
	Error           string
	Message         string
	Readiness       string
	Counts          map[string]int64
	Executions      []executionRow
	Execution       *executionDetail
	ProviderRows    []providerView
	ModelRows       []modelView
	AgentRows       []agentView
	TaskRows        []taskView
	WorkspaceOptions []workspaceOption
	WorkflowRows    []workflowView
	Credential      *credentialView
	FilterStatus    string
	FilterKind      string
	FilterRuntime   string
	FilterTask      string
	Offset          int
	Limit           int
	PrevOffset      int
	NextOffset      int
	HasPrevious     bool
	HasNext         bool
	Attempts        []attemptView
	Artifacts       []artifactView
	Evidence        []evidenceView
	Events          []eventView
	TimelineHasMore bool
	TimelineNextURL string
	RunResult       string
}

type providerView struct{ ID, Name, Type, Kind, BaseURL, Status, CredentialStatus string }
type modelView struct {
	ID, ProviderID, Name, DisplayName, Status, Source string
	ContextWindow, MaxOutputTokens                    int
	Streaming                                         bool
}
type agentRunView struct{ ID, TaskID, Status, CreatedAt, ExecutionID string }
type agentOption struct { ID, Name, Role, Status, Capabilities string }
type taskView struct {
	TaskID, WorkspaceID, ParentTaskID, Title, Type, Status, CreatedAt, ExecutionID string
	Priority int
	AgentOptions []agentOption
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
	t := template.Must(template.New("dashboard").Parse(layoutTemplate + pageTemplate + loginTemplate))
	h := &Handler{
		db:                  d.DB,
		token:               d.APIToken,
		manager:             d.ExecutionManager,
		providerRegistry:    d.Providers,
		modelRegistry:       d.Models,
		credentials:         d.Credentials,
		agentCatalog:        d.AgentDefinitions,
		collaboration:       d.Collaboration,
		workflowEngine:      d.WorkflowEngine,
		workflowMaxAttempts: d.WorkflowMaxAttempts,
		tmpl:                t,
		loginAttempts:       make(map[string][]time.Time),
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
	g.GET("/login", h.loginPage)
	g.POST("/login", h.login)
	g.POST("/logout", h.requireSession(), h.logout)
	g.GET("", h.requireSession(), h.overview)
	g.GET("/", h.requireSession(), h.overview)
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
	g.POST("/agents/:id/update", h.requireSession(), h.updateAgent)
	g.POST("/agents/:id/run", h.requireSession(), h.runAgent)
	g.POST("/agents/:id/enabled", h.requireSession(), h.setAgentEnabled)
	g.POST("/agents/:id/delete", h.requireSession(), h.deleteAgent)
	g.GET("/workflows", h.requireSession(), h.workflows)
	g.POST("/workflows", h.requireSession(), h.createWorkflowTemplate)
	g.POST("/workflows/:id/apply", h.requireSession(), h.applyWorkflowTemplate)
	g.POST("/workflows/:id/run", h.requireSession(), h.runWorkflow)
	g.POST("/workflows/:id/delete", h.requireSession(), h.deleteWorkflowTemplate)
	g.GET("/executions", h.requireSession(), h.executions)
	g.GET("/executions/:id", h.requireSession(), h.execution)
	g.POST("/executions/:id/cancel", h.requireSession(), h.cancelExecution)
}

func (h *Handler) htmxAsset(c *gin.Context) {
	c.Header("Content-Type", "text/javascript; charset=utf-8")
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(http.StatusOK, "text/javascript; charset=utf-8", htmxJS)
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
			"executions": "SELECT COUNT(*) FROM executions",
			"agents":     "SELECT COUNT(*) FROM agent_definitions",
			"providers":  "SELECT COUNT(*) FROM providers",
			"workflows":  "SELECT COUNT(*) FROM workflows",
		}
		for name, query := range queries {
			var count int64
			if err := h.db.QueryRowContext(c.Request.Context(), query).Scan(&count); err == nil {
				data.Counts[name] = count
			}
		}
	}
	h.render(c, data, http.StatusOK)
}

func (h *Handler) callCatalog(c *gin.Context, method, path string, payload any) bool {
	if h.catalogAPI == nil {
		return false
	}
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return false
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req = req.WithContext(c.Request.Context())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.catalogAPI.ServeHTTP(rec, req)
	return rec.Code >= 200 && rec.Code < 300
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
	if !h.callCatalog(c, http.MethodPost, "/api/v1/providers/"+c.Param("id")+"/test", map[string]any{}) {
		c.Redirect(http.StatusSeeOther, "/dashboard/providers?error=provider-test")
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard/providers?message=provider-tested")
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
	c.Redirect(http.StatusSeeOther, "/dashboard/workflows?message=workflow-run-"+string(run.Status))
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

func (h *Handler) agents(c *gin.Context) {
	data := pageData{Title: "Agents", Active: "agents", AgentRows: []agentView{}}
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
			for _, workspace := range workspaces { data.WorkspaceOptions = append(data.WorkspaceOptions, workspaceOption{ID:workspace.ID, Name:workspace.Name}) }
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
	data := pageData{Title: "Workflows", Active: "workflows", WorkflowRows: []workflowView{}}
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
	if c.Query("error") != "" {
		data.Error = "The requested operation could not be completed. Check the submitted values and current object state."
	}
	switch c.Query("message") {
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

const loginTemplate = `{{define "login"}}<section class="panel narrow"><h1>Sign in</h1><p>Enter the local AgentMesh API token. It is never stored in the browser cookie.</p>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}<form method="post" action="/dashboard/login"><label>API token<input type="password" name="token" autocomplete="current-password" required></label><button>Sign in</button></form></section>{{end}}`

const pageTemplate = `{{define "page"}}<section class="panel"><h1>{{.Title}}</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{if .Message}}<p class="notice">{{.Message}}</p>{{end}}
{{if .Readiness}}<p class="notice">{{.Readiness}}</p>{{end}}{{if .Counts}}<div class="stats">{{range $k,$v:=.Counts}}<div><strong>{{$v}}</strong><span>{{$k}}</span></div>{{end}}</div>{{end}}
{{if eq .Active "providers"}}<h2>Provider instances</h2>{{if .ProviderRows}}<table><thead><tr><th>Provider</th><th>Type</th><th>Kind</th><th>Status</th><th>Credential</th><th>Manage credential</th></tr></thead><tbody>{{range .ProviderRows}}<tr><td><strong>{{.Name}}</strong><small>{{.ID}}</small></td><td>{{.Type}}</td><td>{{.Kind}}</td><td>{{.Status}}</td><td>{{.CredentialStatus}}</td><td><form method="post" action="/dashboard/providers/{{.ID}}/update" class="inline-form"><input name="name" value="{{.Name}}" aria-label="Provider name"><input name="type" value="{{.Type}}" aria-label="Provider type"><input name="kind" value="{{.Kind}}" aria-label="Provider kind"><input name="base_url" placeholder="New base URL (leave blank to keep current)" aria-label="Base URL"><input name="api_key_env" placeholder="New API key env (optional)" aria-label="API key environment variable"><button>Update</button></form><form method="post" action="/dashboard/providers/{{.ID}}/enabled"><input type="hidden" name="enabled" value="{{if eq .Status "enabled"}}false{{else}}true{{end}}"><button>{{if eq .Status "enabled"}}Disable{{else}}Enable{{end}}</button></form><form method="post" action="/dashboard/providers/{{.ID}}/test"><button>Test connection</button></form><form method="post" action="/dashboard/providers/{{.ID}}/delete" onsubmit="return confirm('Delete this provider and its configuration?')"><button class="danger">Delete</button></form><form method="post" action="/dashboard/providers/{{.ID}}/credential"><input type="password" name="api_key" placeholder="New API key" autocomplete="new-password" required><button>Save key</button></form><form method="post" action="/dashboard/providers/{{.ID}}/credential/delete" onsubmit="return confirm('Delete stored credential?')"><button class="danger">Delete key</button></form></td></tr>{{end}}</tbody></table>{{else}}<p>No providers are configured.</p>{{end}}<h2>Add provider</h2><form method="post" action="/dashboard/providers" class="form-grid"><label>ID<input name="id" required></label><label>Name<input name="name" required></label><label>Type<select name="type"><option value="openai">OpenAI-compatible</option><option value="anthropic">Anthropic</option><option value="gemini">Gemini</option><option value="ollama">Ollama</option></select></label><label>Kind<select name="kind"><option value="cloud">Cloud</option><option value="local">Local</option></select></label><label>Base URL<input name="base_url"></label><label>API key environment variable<input name="api_key_env" placeholder="Optional, e.g. OPENAI_API_KEY"></label><button>Create provider</button></form>{{end}}
{{if eq .Active "models"}}<h2>Available models</h2>{{if .ModelRows}}<table><thead><tr><th>Model</th><th>Provider</th><th>Source</th><th>Status</th><th>Context</th><th>Max output</th><th>Streaming</th></tr></thead><tbody>{{range .ModelRows}}<tr><td>{{.DisplayName}}<small>{{.ID}}</small></td><td>{{.ProviderID}}</td><td>{{.Source}}</td><td>{{.Status}}</td><td>{{.ContextWindow}}</td><td>{{.MaxOutputTokens}}</td><td>{{.Streaming}}</td><td>{{if eq .Source "provider"}}<form method="post" action="/dashboard/models/{{.ID}}/update" class="inline-form"><input name="provider_id" value="{{.ProviderID}}" aria-label="Provider ID"><input name="name" value="{{.Name}}" aria-label="Model name"><input name="display_name" value="{{.DisplayName}}" aria-label="Display name"><input type="number" name="context_window" min="0" value="{{.ContextWindow}}" aria-label="Context window"><input type="number" name="max_output_tokens" min="0" value="{{.MaxOutputTokens}}" aria-label="Max output tokens"><select name="supports_streaming"><option value="true" {{if .Streaming}}selected{{end}}>Streaming</option><option value="false" {{if not .Streaming}}selected{{end}}>No streaming</option></select><button>Update</button></form><form method="post" action="/dashboard/models/{{.ID}}/enabled"><input type="hidden" name="enabled" value="{{if eq .Status "enabled"}}false{{else}}true{{end}}"><button>{{if eq .Status "enabled"}}Disable{{else}}Enable{{end}}</button></form><form method="post" action="/dashboard/models/{{.ID}}/delete" onsubmit="return confirm('Delete this model?')"><button class="danger">Delete</button></form>{{else}}<span>Managed by AI Resource</span>{{end}}</td></tr>{{end}}</tbody></table>{{else}}<p>No models are registered.</p>{{end}}<h2>Add model</h2><form method="post" action="/dashboard/models" class="form-grid"><label>Model ID<input name="id" required></label><label>Provider ID<input name="provider_id" required></label><label>API model name<input name="name" required></label><label>Display name<input name="display_name"></label><label>Context window<input type="number" name="context_window" min="0" value="0"></label><label>Max output tokens<input type="number" name="max_output_tokens" min="0" value="0"></label><label>Streaming<select name="supports_streaming"><option value="false">No</option><option value="true">Yes</option></select></label><button>Create model</button></form>{{end}}
{{if eq .Active "agents"}}<h2>Agent definitions</h2>{{if .AgentRows}}<table><thead><tr><th>Name</th><th>Model</th><th>Version</th><th>Status</th><th>Description</th></tr></thead><tbody>{{range .AgentRows}}<tr><td>{{.Name}}<small>{{.ID}}</small></td><td>{{.ModelID}}<small>Tools: {{.ApprovedTools}}</small></td><td>{{.Version}}</td><td>{{if .Enabled}}Enabled{{else}}Disabled{{end}}</td><td>{{.Description}}{{if .Runs}}<small>Recent runs</small><ul>{{range .Runs}}<li>{{.Status}} · {{.CreatedAt}} {{if .ExecutionID}}<a href="/dashboard/executions/{{.ExecutionID}}">Execution</a>{{end}}</li>{{end}}</ul>{{end}}</td><td><form method="post" action="/dashboard/agents/{{.ID}}/update" class="inline-form"><input name="name" value="{{.Name}}" aria-label="Agent name"><input name="description" value="{{.Description}}" aria-label="Description"><input name="model_id" value="{{.ModelID}}" aria-label="Model ID"><input type="number" name="temperature" min="0" max="2" step="0.1" value="{{.Temperature}}" aria-label="Temperature"><input type="number" name="max_steps" min="0" value="{{.MaxSteps}}" aria-label="Max steps"><input type="number" name="max_tool_calls" min="0" value="{{.MaxToolCalls}}" aria-label="Max tool calls"><input type="number" name="max_output_tokens" min="0" value="{{.MaxOutputTokens}}" aria-label="Max output tokens"><input type="number" name="timeout_seconds" min="0" max="3600" value="{{.TimeoutSeconds}}" aria-label="Timeout seconds"><input name="approved_tools" value="{{.ApprovedTools}}" aria-label="Approved tools"><textarea name="system_prompt" rows="4" aria-label="System prompt">{{.SystemPrompt}}</textarea><button>Update definition</button></form><form method="post" action="/dashboard/agents/{{.ID}}/enabled"><input type="hidden" name="enabled" value="{{if .Enabled}}false{{else}}true{{end}}"><button>{{if .Enabled}}Disable{{else}}Enable{{end}}</button></form><form method="post" action="/dashboard/agents/{{.ID}}/run" class="run-form"><input name="workspace_id" placeholder="Workspace ID" required><input name="prompt" placeholder="Task prompt" required><button>Run agent</button></form><form method="post" action="/dashboard/agents/{{.ID}}/delete" onsubmit="return confirm('Delete this agent definition?')"><button class="danger">Delete</button></form></td></tr>{{end}}</tbody></table>{{else}}<p>No agent definitions are configured.</p>{{end}}<h2>Create agent definition</h2><form method="post" action="/dashboard/agents" class="form-grid"><label>ID<input name="id" required></label><label>Name<input name="name" required></label><label>Model ID<input name="model_id" required></label><label>Description<input name="description"></label><label>Temperature<input type="number" step="0.1" min="0" max="2" name="temperature" value="0.2"></label><label>Max steps<input type="number" min="0" name="max_steps" value="10"></label><label>Max tool calls<input type="number" min="0" name="max_tool_calls" value="20"></label><label>Max output tokens<input type="number" min="0" name="max_output_tokens" value="2048"></label><label>Timeout seconds<input type="number" min="0" max="3600" name="timeout_seconds" value="300"></label><label>Approved tools<input name="approved_tools" placeholder="read_file, write_file"></label><label class="wide">System prompt<textarea name="system_prompt" required></textarea></label><button>Create agent</button></form>{{end}}
{{if eq .Active "tasks"}}<h2>Collaboration Tasks</h2>{{if .TaskRows}}<table><thead><tr><th>Task</th><th>Workspace</th><th>Type</th><th>Priority</th><th>Status</th><th>Parent</th><th>Created</th><th>Execution</th><th>Assign</th></tr></thead><tbody>{{range .TaskRows}}<tr><td><strong>{{.Title}}</strong><small>{{.TaskID}}</small></td><td>{{.WorkspaceID}}</td><td>{{.Type}}</td><td>{{.Priority}}</td><td>{{.Status}}</td><td>{{.ParentTaskID}}</td><td>{{.CreatedAt}}</td><td>{{if .ExecutionID}}<a href="/dashboard/executions/{{.ExecutionID}}">Open execution</a>{{else}}—{{end}}</td><td><form method="post" action="/dashboard/tasks/{{.TaskID}}/assign"><select name="agent_id" required><option value="">Select Agent Identity</option>{{range .AgentOptions}}<option value="{{.ID}}">{{.Name}} · {{.Role}} · {{.Status}}</option>{{end}}</select><button>Assign</button></form></td></tr>{{end}}</tbody></table>{{else}}<p>No Collaboration tasks are available.</p>{{end}}<h2>Create task</h2><form method="post" action="/dashboard/tasks" class="form-grid"><label>Workspace<select name="workspace_id" required><option value="">Select workspace</option>{{range .WorkspaceOptions}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label><label>Parent Task ID<input name="parent_task_id"></label><label>Title<input name="title" required></label><label>Type<input name="type" value="custom"></label><label>Priority (1–5)<input type="number" name="priority" min="1" max="5" value="3" required></label><label>Idempotency key<input name="idempotency_key"></label><label>Required capabilities<input name="required_capabilities" placeholder="coding, testing"></label><label class="wide">Description<textarea name="description" rows="3"></textarea></label><button>Create task</button></form>{{end}}
{{if eq .Active "workflows"}}<h2>Workflow templates</h2>{{if .WorkflowRows}}<table><thead><tr><th>Name</th><th>ID</th><th>Version</th><th>Updated</th><th>Description</th></tr></thead><tbody>{{range .WorkflowRows}}<tr><td>{{.Name}}</td><td>{{.ID}}</td><td>{{.Version}}</td><td>{{.UpdatedAt}}</td><td>{{.Description}}{{if .Runs}}<small>Recent runs</small><ul>{{range .Runs}}<li>{{.Status}} · {{.CreatedAt}} {{if .ExecutionID}}<a href="/dashboard/executions/{{.ExecutionID}}">Execution</a>{{end}}</li>{{end}}</ul>{{end}}</td><td><form method="post" action="/dashboard/workflows/{{.ID}}/apply"><input name="workflow_id" value="{{.ID}}" aria-label="Workflow ID"><input type="number" name="version" min="1" value="{{.Version}}" aria-label="Version"><button>Apply</button></form><form method="post" action="/dashboard/workflows/{{.ID}}/run"><label>Input JSON<textarea name="input_json" rows="2">{}</textarea></label><button>Queue run</button></form><form method="post" action="/dashboard/workflows/{{.ID}}/delete" onsubmit="return confirm('Delete this template?')"><button class="danger">Delete</button></form></td></tr>{{end}}</tbody></table>{{else}}<p>No workflow templates are saved.</p>{{end}}<h2>Save workflow template</h2><form method="post" action="/dashboard/workflows" class="form-grid"><label>Name<input name="name" required></label><label>Description<input name="description"></label><label class="wide">Workflow definition JSON<textarea name="definition_json" rows="12" required placeholder='{"id":"example","name":"Example","version":1,"entry":"start","nodes":[],"edges":[]}'></textarea></label><button>Save template</button></form>{{end}}
{{if eq .Active "executions"}}<form method="get" action="/dashboard/executions" class="filters"><label>Status<input name="status" value="{{.FilterStatus}}" placeholder="running, failed..."></label><label>Kind<input name="kind" value="{{.FilterKind}}" placeholder="agent, coding..."></label><label>Runtime<input name="runtime" value="{{.FilterRuntime}}"></label><label>Task ID<input name="task" value="{{.FilterTask}}"></label><button>Filter</button></form>{{if .Executions}}<table><thead><tr><th>ID</th><th>Kind</th><th>Runtime</th><th>Status</th><th>Created</th></tr></thead><tbody>{{range .Executions}}<tr><td><a href="/dashboard/executions/{{.ID}}">{{.ID}}</a></td><td>{{.Kind}}</td><td>{{.Runtime}}</td><td>{{.Status}}</td><td>{{.CreatedAt}}</td></tr>{{end}}</tbody></table>{{else}}<p>No executions match the current filters.</p>{{end}}{{if or .HasPrevious .HasNext}}<nav class="pagination">{{if .HasPrevious}}<a href="/dashboard/executions?status={{.FilterStatus}}&amp;kind={{.FilterKind}}&amp;runtime={{.FilterRuntime}}&amp;task={{.FilterTask}}&amp;limit={{.Limit}}&amp;offset={{.PrevOffset}}">Previous</a>{{end}}<span>Offset {{.Offset}}</span>{{if .HasNext}}<a href="/dashboard/executions?status={{.FilterStatus}}&amp;kind={{.FilterKind}}&amp;runtime={{.FilterRuntime}}&amp;limit={{.Limit}}&amp;offset={{.NextOffset}}">Next</a>{{end}}</nav>{{end}}{{end}}
{{if .Execution}}<dl><dt>Kind</dt><dd>{{.Execution.Kind}}</dd><dt>Runtime</dt><dd>{{.Execution.Runtime}}</dd><dt>Status</dt><dd>{{.Execution.Status}}</dd><dt>Task</dt><dd>{{.Execution.TaskID}}</dd><dt>Created</dt><dd>{{.Execution.CreatedAt}}</dd><dt>Updated</dt><dd>{{.Execution.UpdatedAt}}</dd></dl>
{{if or (eq .Execution.Status "queued") (eq .Execution.Status "running")}}<form method="post" action="/dashboard/executions/{{.Execution.ID}}/cancel" onsubmit="return confirm('Request cancellation for this execution?')"><button class="danger">Request cancellation</button></form>{{end}}
<h2>Attempts</h2>{{if .Attempts}}<table><tr><th>Attempt ID</th><th>Attempt</th><th>Runtime</th><th>Status</th><th>Started</th><th>Finished</th></tr>{{range .Attempts}}<tr><td>{{.ID}}</td><td>{{.Number}}</td><td>{{.Runtime}}</td><td>{{.Status}}</td><td>{{.StartedAt}}</td><td>{{.FinishedAt}}</td></tr>{{end}}</table>{{else}}<p>No attempts recorded.</p>{{end}}
<h2>Artifact versions</h2>{{if .Artifacts}}<table><tr><th>Artifact</th><th>Version</th><th>Media type</th><th>Size</th><th>SHA-256</th><th>Source execution</th><th>Source attempt</th></tr>{{range .Artifacts}}<tr><td>{{.ArtifactID}}</td><td>{{.Version}}</td><td>{{.MediaType}}</td><td>{{.Size}}</td><td>{{.Hash}}</td><td>{{.SourceExecutionID}}</td><td>{{.SourceAttemptID}}</td></tr>{{end}}</table>{{else}}<p>No artifact versions linked.</p>{{end}}
<h2>Evidence</h2>{{if .Evidence}}<table><tr><th>Evidence ID</th><th>Kind</th><th>Artifact</th><th>Version</th><th>Execution</th><th>Attempt</th></tr>{{range .Evidence}}<tr><td>{{.ID}}</td><td>{{.Kind}}</td><td>{{.ArtifactID}}</td><td>{{.VersionID}}</td><td>{{.ExecutionID}}</td><td>{{.AttemptID}}</td></tr>{{end}}</table>{{else}}<p>No evidence linked.</p>{{end}}
<h2>Timeline</h2>{{if .Events}}<ol>{{range .Events}}<li><time>{{.OccurredAt}}</time> <strong>{{.Stream}} · {{.Type}}</strong> — {{.Actor}} / {{.Source}}</li>{{end}}</ol>{{else}}<p>No execution events recorded.</p>{{end}}{{if .TimelineHasMore}}<a href="{{.TimelineNextURL}}">Load more events</a>{{end}}{{end}}
</section>{{end}}`

const layoutTemplate = `{{define "layout"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><script src="/dashboard/assets/htmx.min.js" defer></script><title>{{.Title}} · AgentMesh</title><style>
:root{color-scheme:dark;font:15px/1.5 Inter,system-ui,sans-serif;background:#0b1020;color:#e8ecf5}*{box-sizing:border-box}body{margin:0}.shell{display:grid;grid-template-columns:230px 1fr;min-height:100vh}aside{padding:28px 18px;background:#111a2e;border-right:1px solid #27334b}aside h2{font-size:18px;margin:0 0 28px}nav{display:grid;gap:6px}nav a{padding:10px 12px;border-radius:8px;color:#aebbd2;text-decoration:none}nav a.active,nav a:hover{background:#24324e;color:#fff}main{padding:38px;max-width:1200px;width:100%}.panel{background:#121c31;border:1px solid #293650;border-radius:14px;padding:26px;min-height:180px}.panel h1{margin-top:0}.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(130px,1fr));gap:14px}.stats div{padding:18px;background:#1b2942;border-radius:10px;display:grid}.stats strong{font-size:28px}.stats span,dt{color:#9eacc4;text-transform:capitalize}table{width:100%;border-collapse:collapse;margin-top:20px}th,td{text-align:left;padding:12px;border-bottom:1px solid #293650}a{color:#8eb8ff}label{display:grid;gap:8px}textarea{padding:12px;background:#0b1020;color:white;border:1px solid #40506c;border-radius:8px;min-height:110px} .wide{grid-column:1/-1}input{padding:12px;background:#0b1020;color:white;border:1px solid #40506c;border-radius:8px}.inline-form{display:grid;gap:6px;margin:8px 0}.form-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px;margin:18px 0}.form-grid label{display:grid;gap:6px}.pagination{display:flex;gap:18px;align-items:center;margin-top:18px}.run-form{display:grid;gap:8px;margin:12px 0}.run-form input{width:100%}small{display:block;color:#9eacc4;font-size:12px}button{margin-top:14px;padding:10px 16px;border:0;border-radius:8px;background:#477fe0;color:white;cursor:pointer}.danger{background:#a84352}.error{color:#ff9b9b}.notice{color:#9ec7ff}.narrow{max-width:440px;margin:10vh auto}dl{display:grid;grid-template-columns:150px 1fr;gap:10px}dd{margin:0;overflow-wrap:anywhere}@media(max-width:760px){.shell{grid-template-columns:1fr}aside{border-right:0;border-bottom:1px solid #27334b;padding:16px}nav{grid-template-columns:repeat(2,1fr)}main{padding:18px}.panel{padding:18px}table{font-size:13px;display:block;overflow-x:auto}}
</style></head><body hx-boost="true"><div class="shell"><aside><h2>◈ AgentMesh</h2><nav><a href="/dashboard" class="{{if eq .Active "overview"}}active{{end}}">Overview</a><a href="/dashboard/providers" class="{{if eq .Active "providers"}}active{{end}}">Providers</a><a href="/dashboard/models" class="{{if eq .Active "models"}}active{{end}}">Models</a><a href="/dashboard/agents" class="{{if eq .Active "agents"}}active{{end}}">Agents</a><a href="/dashboard/tasks" class="{{if eq .Active "tasks"}}active{{end}}">Tasks</a><a href="/dashboard/workflows" class="{{if eq .Active "workflows"}}active{{end}}">Workflows</a><a href="/dashboard/executions" class="{{if eq .Active "executions"}}active{{end}}">Executions</a></nav>{{if ne .Title "Sign in"}}<form method="post" action="/dashboard/logout"><button>Sign out</button></form>{{end}}</aside><main>{{if eq .Title "Sign in"}}{{template "login" .}}{{else}}{{template "page" .}}{{end}}</main></div></body></html>{{end}}`
