package dashboard

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/agentcatalog"
	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	domainprovider "github.com/thkx/agentmesh/internal/domain/provider"
	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/execution/manager"
	"github.com/thkx/agentmesh/internal/workflow/authoring"
)

const sessionCookie = "agentmesh_dashboard"

type Dependencies struct {
	DB               *sql.DB
	APIToken         string
	ExecutionManager *manager.Manager
	Providers *domainprovider.ProviderRegistry
	Models *domainprovider.ModelRegistry
	Credentials appcredential.Manager
	AgentDefinitions *agentcatalog.Service
}

type Handler struct {
	db      *sql.DB
	token   string
	manager *manager.Manager
	providers *domainprovider.ProviderRegistry
	models *domainprovider.ModelRegistry
	credentials appcredential.Manager
	agents *agentcatalog.Service
	workflowRepo *authoring.Repository
	tmpl    *template.Template
}

type pageData struct {
	Title      string
	Active     string
	Error      string
	Message    string
	Counts     map[string]int64
	Executions []executionRow
	Execution  *executionDetail
	ProviderRows []providerView
	ModelRows []modelView
	AgentRows []agentView
	WorkflowRows []workflowView
	Credential *credentialView
	FilterStatus string
	FilterKind string
	FilterRuntime string
	Attempts []attemptView
	Artifacts []artifactView
	Evidence []evidenceView
	Events []eventView
}

type providerView struct { ID, Name, Type, Kind, BaseURL, Status, CredentialStatus string }
type modelView struct { ID, ProviderID, Name, DisplayName, Status, Source string; ContextWindow, MaxOutputTokens int; Streaming bool }
type agentView struct { ID, Name, ModelID, Description string; Enabled bool; Version int }
type workflowView struct { ID, Name, Description string; Version int; UpdatedAt string }
type credentialView struct { Source, Status string; UpdatedAt string }
type attemptView struct { ID, Runtime, Status string; Number int; StartedAt string; FinishedAt string }
type artifactView struct { ID, ArtifactID string; Version int; MediaType, Hash string; Size int64 }
type evidenceView struct { ID, ArtifactID, VersionID, Kind, Statement string }
type eventView struct { Stream, Type, Actor, Source, Payload, OccurredAt string; Sequence int64 }

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
		db:      d.DB,
		token:   d.APIToken,
		manager: d.ExecutionManager,
		providers: d.Providers,
		models: d.Models,
		credentials: d.Credentials,
		agents: d.AgentDefinitions,
		tmpl: t,
	}
	if d.DB != nil {
		h.workflowRepo, _ = authoring.NewRepository(d.DB)
	}
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	g := r.Group("/dashboard")
	g.GET("/login", h.loginPage)
	g.POST("/login", h.login)
	g.POST("/logout", h.requireSession(), h.logout)
	g.GET("", h.requireSession(), h.overview)
	g.GET("/", h.requireSession(), h.overview)
	g.GET("/providers", h.requireSession(), h.providers)
	g.POST("/providers/:id/credential", h.requireSession(), h.setCredential)
	g.POST("/providers/:id/credential/delete", h.requireSession(), h.deleteCredential)
	g.GET("/models", h.requireSession(), h.models)
	g.GET("/agents", h.requireSession(), h.agents)
	g.GET("/workflows", h.requireSession(), h.workflows)
	g.GET("/executions", h.requireSession(), h.executions)
	g.GET("/executions/:id", h.requireSession(), h.execution)
	g.POST("/executions/:id/cancel", h.requireSession(), h.cancelExecution)
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

func (h *Handler) login(c *gin.Context) {
	if h.token == "" {
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	if !sameOrigin(c.Request) || (!isLoopbackRequest(c.Request) && c.Request.TLS == nil) {
		c.AbortWithStatus(http.StatusForbidden)
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
		Counts: map[string]int64{},
	}
	if h.db != nil {
		queries := map[string]string{
			"executions": "SELECT COUNT(*) FROM executions",
			"agents":     "SELECT COUNT(*) FROM agent_definitions",
			"providers":  "SELECT COUNT(*) FROM providers",
			"workflows":  "SELECT COUNT(*) FROM workflow_definitions",
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

func (h *Handler) providers(c *gin.Context) {
	h.render(c, pageData{
		Title: "Providers", Active: "providers",
		Message: "Provider configuration is available through the existing API. Credential values are write-only and are never rendered here.",
	}, http.StatusOK)
}

func (h *Handler) models(c *gin.Context) {
	h.render(c, pageData{
		Title: "Models", Active: "models",
		Message: "Model catalog is available through the existing API and dynamic AI Resource catalog.",
	}, http.StatusOK)
}

func (h *Handler) agents(c *gin.Context) {
	h.render(c, pageData{
		Title: "Agents", Active: "agents",
		Message: "Agent definitions and lifecycle operations remain owned by the Agent Definition service.",
	}, http.StatusOK)
}

func (h *Handler) workflows(c *gin.Context) {
	h.render(c, pageData{
		Title: "Workflows", Active: "workflows",
		Message: "Workflow definitions and authoring remain owned by the Workflow Engine and Authoring repository.",
	}, http.StatusOK)
}

func (h *Handler) executions(c *gin.Context) {
	data := pageData{
		Title:      "Executions",
		Active:     "executions",
		Executions: []executionRow{},
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

	query := `SELECT id,kind,runtime,COALESCE(task_id,''),status,created_at,updated_at
		FROM executions ORDER BY created_at DESC LIMIT ? OFFSET ?`
	rows, err := h.db.QueryContext(c.Request.Context(), query, limit, offset)
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
	h.render(c, pageData{
		Title: "Execution " + item.ID, Active: "executions", Execution: &item,
	}, http.StatusOK)
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
		data.Error = "The requested operation could not be completed."
	}
	if c.Query("message") == "cancel-requested" {
		data.Message = "Cancellation request recorded; the runtime outcome is not yet confirmed."
	}
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

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

const loginTemplate = `{{define "login"}}<section class="panel narrow"><h1>Sign in</h1><p>Enter the local AgentMesh API token. It is never stored in the browser cookie.</p>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}<form method="post" action="/dashboard/login"><label>API token<input type="password" name="token" autocomplete="current-password" required></label><button>Sign in</button></form></section>{{end}}`

const pageTemplate = `{{define "page"}}<section class="panel"><h1>{{.Title}}</h1>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{if .Message}}<p class="notice">{{.Message}}</p>{{end}}{{if .Counts}}<div class="stats">{{range $k,$v:=.Counts}}<div><strong>{{$v}}</strong><span>{{$k}}</span></div>{{end}}</div>{{end}}{{if .Executions}}<table><thead><tr><th>ID</th><th>Kind</th><th>Runtime</th><th>Status</th><th>Created</th></tr></thead><tbody>{{range .Executions}}<tr><td><a href="/dashboard/executions/{{.ID}}">{{.ID}}</a></td><td>{{.Kind}}</td><td>{{.Runtime}}</td><td>{{.Status}}</td><td>{{.CreatedAt}}</td></tr>{{end}}</tbody></table>{{else if .Execution}}<dl><dt>Kind</dt><dd>{{.Execution.Kind}}</dd><dt>Runtime</dt><dd>{{.Execution.Runtime}}</dd><dt>Status</dt><dd>{{.Execution.Status}}</dd><dt>Task</dt><dd>{{.Execution.TaskID}}</dd><dt>Created</dt><dd>{{.Execution.CreatedAt}}</dd><dt>Updated</dt><dd>{{.Execution.UpdatedAt}}</dd></dl>{{if or (eq .Execution.Status "queued") (eq .Execution.Status "running")}}<form method="post" action="/dashboard/executions/{{.Execution.ID}}/cancel" onsubmit="return confirm('Request cancellation for this execution?')"><button class="danger">Request cancellation</button></form>{{end}}{{end}}</section>{{end}}`

const layoutTemplate = `{{define "layout"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} · AgentMesh</title><style>
:root{color-scheme:dark;font:15px/1.5 Inter,system-ui,sans-serif;background:#0b1020;color:#e8ecf5}*{box-sizing:border-box}body{margin:0}.shell{display:grid;grid-template-columns:230px 1fr;min-height:100vh}aside{padding:28px 18px;background:#111a2e;border-right:1px solid #27334b}aside h2{font-size:18px;margin:0 0 28px}nav{display:grid;gap:6px}nav a{padding:10px 12px;border-radius:8px;color:#aebbd2;text-decoration:none}nav a.active,nav a:hover{background:#24324e;color:#fff}main{padding:38px;max-width:1200px;width:100%}.panel{background:#121c31;border:1px solid #293650;border-radius:14px;padding:26px;min-height:180px}.panel h1{margin-top:0}.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(130px,1fr));gap:14px}.stats div{padding:18px;background:#1b2942;border-radius:10px;display:grid}.stats strong{font-size:28px}.stats span,dt{color:#9eacc4;text-transform:capitalize}table{width:100%;border-collapse:collapse;margin-top:20px}th,td{text-align:left;padding:12px;border-bottom:1px solid #293650}a{color:#8eb8ff}label{display:grid;gap:8px}input{padding:12px;background:#0b1020;color:white;border:1px solid #40506c;border-radius:8px}button{margin-top:14px;padding:10px 16px;border:0;border-radius:8px;background:#477fe0;color:white;cursor:pointer}.danger{background:#a84352}.error{color:#ff9b9b}.notice{color:#9ec7ff}.narrow{max-width:440px;margin:10vh auto}dl{display:grid;grid-template-columns:150px 1fr;gap:10px}dd{margin:0;overflow-wrap:anywhere}@media(max-width:760px){.shell{grid-template-columns:1fr}aside{border-right:0;border-bottom:1px solid #27334b;padding:16px}nav{grid-template-columns:repeat(2,1fr)}main{padding:18px}.panel{padding:18px}table{font-size:13px;display:block;overflow-x:auto}}
</style></head><body><div class="shell"><aside><h2>◈ AgentMesh</h2><nav><a href="/dashboard" class="{{if eq .Active "overview"}}active{{end}}">Overview</a><a href="/dashboard/providers" class="{{if eq .Active "providers"}}active{{end}}">Providers</a><a href="/dashboard/models" class="{{if eq .Active "models"}}active{{end}}">Models</a><a href="/dashboard/agents" class="{{if eq .Active "agents"}}active{{end}}">Agents</a><a href="/dashboard/workflows" class="{{if eq .Active "workflows"}}active{{end}}">Workflows</a><a href="/dashboard/executions" class="{{if eq .Active "executions"}}active{{end}}">Executions</a></nav>{{if ne .Title "Sign in"}}<form method="post" action="/dashboard/logout"><button>Sign out</button></form>{{end}}</aside><main>{{if eq .Title "Sign in"}}{{template "login" .}}{{else}}{{template "page" .}}{{end}}</main></div></body></html>{{end}}`
