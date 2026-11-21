package dashboard

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/observability"
	_ "modernc.org/sqlite"
)

func testRouter(token string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(Dependencies{APIToken: token}).RegisterRoutes(r)
	return r
}

func TestDashboardRendersWithoutExternalAssets(t *testing.T) {
	r := testRouter("")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"AgentMesh", "Overview", "Providers", "Executions", "Your first AgentMesh workflow", "Connect a model provider", "Run your first Agent task"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("dashboard response missing %q", want)
		}
	}
}

func TestDashboardRequiresBrowserSessionWhenAPITokenConfigured(t *testing.T) {
	r := testRouter("secret-api-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard/login" {
		t.Fatalf("expected login redirect, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestDashboardLoginIssuesOpaqueSignedCookie(t *testing.T) {
	const token = "secret-api-token"
	r := testRouter(token)
	login := httptest.NewRecorder()
	r.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/dashboard/login", nil))
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), "API token") {
		t.Fatalf("login page failed: %d %s", login.Code, login.Body.String())
	}

	form := strings.NewReader("token=" + token)
	req := httptest.NewRequest(http.MethodPost, "/dashboard/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1")
	req.Host = "127.0.0.1"
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookie {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("session cookie was not set")
	}
	if strings.Contains(session.Value, token) || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe session cookie: %#v", session)
	}

	page := httptest.NewRecorder()
	pageReq := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	pageReq.AddCookie(session)
	r.ServeHTTP(page, pageReq)
	if page.Code != http.StatusOK {
		t.Fatalf("authenticated dashboard status = %d", page.Code)
	}
}

func TestDashboardRejectsCrossOriginLogin(t *testing.T) {
	r := testRouter("secret-api-token")
	req := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("token=secret-api-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://attacker.example")
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d", rec.Code)
	}
}

func TestDashboardServesVendoredHTMXAsset(t *testing.T) {
	r := testRouter("")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/assets/htmx.min.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status = %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "javascript") || !strings.Contains(rec.Body.String(), "htmx") {
		t.Fatal("vendored HTMX asset was not served")
	}
}

func TestDashboardServesStylesheetFromEmbeddedWebAssets(t *testing.T) {
	r := testRouter("")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/assets/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("stylesheet status = %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/css") || !strings.Contains(rec.Body.String(), ".shell") {
		t.Fatal("dashboard stylesheet was not served from embedded web assets")
	}
}

func TestDashboardLoginRateLimit(t *testing.T) {
	r := testRouter("expected-token")
	for attempt := 1; attempt <= 6; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("token=wrong"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://127.0.0.1")
		req.Host = "127.0.0.1"
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if attempt <= 5 && rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected unauthorized, got %d", attempt, rec.Code)
		}
		if attempt == 6 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: expected rate limit, got %d", attempt, rec.Code)
		}
	}
}

func TestDashboardRejectsTamperedAndExpiredSessions(t *testing.T) {
	const token = "session-token"
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(Dependencies{APIToken: token})
	h.RegisterRoutes(r)

	loginReq := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("token="+token))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Origin", "http://127.0.0.1")
	loginReq.Host = "127.0.0.1"
	loginReq.RemoteAddr = "127.0.0.1:12345"
	loginRec := httptest.NewRecorder()
	r.ServeHTTP(loginRec, loginReq)
	var session *http.Cookie
	for _, cookie := range loginRec.Result().Cookies() {
		if cookie.Name == sessionCookie {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("login did not issue a session cookie")
	}

	tampered := *session
	parts := strings.Split(tampered.Value, ".")
	if len(parts) != 2 || len(parts[1]) == 0 {
		t.Fatalf("unexpected session cookie format: %q", tampered.Value)
	}
	replacement := byte('A')
	if parts[1][0] == replacement {
		replacement = 'B'
	}
	parts[1] = string(replacement) + parts[1][1:]
	tampered.Value = strings.Join(parts, ".")
	tamperedReq := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	tamperedReq.AddCookie(&tampered)
	tamperedRec := httptest.NewRecorder()
	r.ServeHTTP(tamperedRec, tamperedReq)
	if tamperedRec.Code != http.StatusSeeOther {
		t.Fatalf("tampered session status = %d", tamperedRec.Code)
	}

	expiredReq := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	expiredReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: "1.invalid"})
	expiredRec := httptest.NewRecorder()
	r.ServeHTTP(expiredRec, expiredReq)
	if expiredRec.Code != http.StatusSeeOther {
		t.Fatalf("expired session status = %d", expiredRec.Code)
	}
}

func TestDashboardRejectsCrossOriginLogout(t *testing.T) {
	const token = "logout-token"
	r := testRouter(token)
	loginReq := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("token="+token))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.Header.Set("Origin", "http://127.0.0.1")
	loginReq.Host = "127.0.0.1"
	loginReq.RemoteAddr = "127.0.0.1:12345"
	loginRec := httptest.NewRecorder()
	r.ServeHTTP(loginRec, loginReq)
	var session *http.Cookie
	for _, cookie := range loginRec.Result().Cookies() {
		if cookie.Name == sessionCookie {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("login did not issue a session cookie")
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/dashboard/logout", nil)
	logoutReq.Header.Set("Origin", "https://attacker.example")
	logoutReq.Host = "127.0.0.1"
	logoutReq.AddCookie(session)
	logoutRec := httptest.NewRecorder()
	r.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin logout status = %d", logoutRec.Code)
	}
}

func TestDashboardOptionalDependenciesRenderEmptyStates(t *testing.T) {
	r := testRouter("")
	for _, path := range []string{"/dashboard/providers", "/dashboard/models", "/dashboard/agents", "/dashboard/tasks", "/dashboard/workflows", "/dashboard/executions", "/dashboard/coding"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestDashboardOperationsMetricsPage(t *testing.T) {
	metrics := observability.NewMetrics()
	metrics.ObserveHTTPRequest(http.MethodGet, "/api/v1/models", http.StatusOK, 20*time.Millisecond)
	metrics.ObserveModelGateway("generate", "success", 100*time.Millisecond)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(Dependencies{Metrics: metrics}).RegisterRoutes(router)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/operations/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("operations metrics status = %d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Operations Metrics", "HTTP requests", "Model Gateway requests", "Prometheus text format"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("operations metrics page missing %q", want)
		}
	}
}

func TestDashboardOperationsShowsActiveAndResolvedAlerts(t *testing.T) {
	metrics := observability.NewMetrics()
	alerts := observability.NewAlertEvaluator(10)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(Dependencies{Metrics: metrics, Alerts: alerts}).RegisterRoutes(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/operations/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Database unavailable") || !strings.Contains(rec.Body.String(), "Runtime worker stopped") {
		t.Fatalf("operational alerts not rendered: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDashboardGracefulShutdownRequiresSameOriginAndConfirmation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requests := make(chan struct{}, 1)
	router := gin.New()
	New(Dependencies{RequestShutdown: requests}).RegisterRoutes(router)

	page := httptest.NewRecorder()
	router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/dashboard/operations/metrics", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Gracefully stop AgentMesh") {
		t.Fatalf("operations page missing shutdown action: status=%d", page.Code)
	}

	crossOrigin := httptest.NewRequest(http.MethodPost, "/dashboard/operations/shutdown", strings.NewReader("confirm=yes"))
	crossOrigin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	crossOrigin.Header.Set("Origin", "https://attacker.example")
	crossOrigin.Host = "127.0.0.1"
	rejected := httptest.NewRecorder()
	router.ServeHTTP(rejected, crossOrigin)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("cross-origin shutdown status=%d", rejected.Code)
	}

	unconfirmed := httptest.NewRequest(http.MethodPost, "/dashboard/operations/shutdown", strings.NewReader("confirm=no"))
	unconfirmed.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unconfirmed.Header.Set("Origin", "http://127.0.0.1")
	unconfirmed.Host = "127.0.0.1"
	unconfirmedRec := httptest.NewRecorder()
	router.ServeHTTP(unconfirmedRec, unconfirmed)
	if unconfirmedRec.Code != http.StatusForbidden {
		t.Fatalf("unconfirmed shutdown status=%d", unconfirmedRec.Code)
	}

	confirmed := httptest.NewRequest(http.MethodPost, "/dashboard/operations/shutdown", strings.NewReader("confirm=yes"))
	confirmed.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirmed.Header.Set("Origin", "http://127.0.0.1")
	confirmed.Host = "127.0.0.1"
	confirmedRec := httptest.NewRecorder()
	router.ServeHTTP(confirmedRec, confirmed)
	if confirmedRec.Code != http.StatusSeeOther {
		t.Fatalf("confirmed shutdown status=%d body=%s", confirmedRec.Code, confirmedRec.Body.String())
	}
	select {
	case <-requests:
	default:
		t.Fatal("graceful shutdown request was not sent")
	}
}

func TestOperationsDashboardShowsSQLiteAndWorkerHealth(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE schema_migrations(version INTEGER,dirty BOOLEAN); INSERT INTO schema_migrations(version,dirty) VALUES(44,0); CREATE TABLE executions(id TEXT,status TEXT);"); err != nil {
		t.Fatal(err)
	}
	metrics := observability.NewMetrics()
	metrics.AddRuntimeWorkers(1)
	router := gin.New()
	New(Dependencies{DB: db, Metrics: metrics}).RegisterRoutes(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/operations/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("operations status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"System health", "SQLite integrity", "Schema version", "44", "running", "Gracefully stop AgentMesh"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("operations dashboard missing %q", want)
		}
	}
}

func TestCodingDashboardRendersTaskCreationAndRecentTasks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(Dependencies{SandboxImage: "golang:1.27", SandboxNetworkEnabled: false}).RegisterRoutes(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/coding", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("coding dashboard status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Coding", "Create and run Coding Task", "coding.allowed_repository_roots", "Docker image", "Network mode", "Recent coding tasks"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("coding dashboard missing %q", want)
		}
	}
}

func TestOverviewShowsFirstIncompleteSetupStepAndCountsSuccessfulAgentRuns(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema := `
		CREATE TABLE providers(id TEXT PRIMARY KEY, enabled INTEGER NOT NULL);
		CREATE TABLE models(id TEXT PRIMARY KEY, provider_id TEXT NOT NULL, enabled INTEGER NOT NULL);
		CREATE TABLE agent_definitions(id TEXT PRIMARY KEY, enabled INTEGER NOT NULL);
		CREATE TABLE executions(id TEXT PRIMARY KEY, kind TEXT NOT NULL, status TEXT NOT NULL);
		CREATE TABLE agent_runs(id TEXT PRIMARY KEY, status TEXT NOT NULL);
		CREATE TABLE workflows(id TEXT PRIMARY KEY);
		INSERT INTO providers(id, enabled) VALUES ('provider-1', 1);
		INSERT INTO models(id, provider_id, enabled) VALUES ('model-1', 'provider-1', 1);
		INSERT INTO agent_definitions(id, enabled) VALUES ('agent-1', 1);
		INSERT INTO executions(id, kind, status) VALUES
			('workflow-run', 'workflow', 'succeeded');
		INSERT INTO agent_runs(id, status) VALUES
			('agent-queued', 'queued'),
			('agent-failed', 'failed');
		INSERT INTO workflows(id) VALUES ('workflow-1');
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	New(Dependencies{DB: db}).RegisterRoutes(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("overview status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"4 / 5",
		"href=\"/dashboard/new-task\">Start a task</a>",
		"已完成 Agent 任务",
		"Recommended next step: Run your first Agent task",
		"href=\"/dashboard/agents\">Continue</a>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("overview missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "agent_executions</span></div>") {
		t.Fatal("agent execution counter should not be shown as a separate setup requirement")
	}
	if _, err = db.Exec("INSERT INTO agent_runs(id, status) VALUES ('agent-succeeded', 'succeeded')"); err != nil {
		t.Fatal(err)
	}
	completed := httptest.NewRecorder()
	router.ServeHTTP(completed, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if completed.Code != http.StatusOK || !strings.Contains(completed.Body.String(), "5 / 5") || !strings.Contains(completed.Body.String(), "Your first-run setup is complete") {
		t.Fatalf("successful Agent run did not complete onboarding: status=%d body=%s", completed.Code, completed.Body.String())
	}
}

func TestOverviewEmptyDatabaseRecommendsConnectingProvider(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"CREATE TABLE providers(id TEXT PRIMARY KEY, enabled INTEGER NOT NULL)",
		"CREATE TABLE models(id TEXT PRIMARY KEY, provider_id TEXT NOT NULL, enabled INTEGER NOT NULL)",
		"CREATE TABLE agent_definitions(id TEXT PRIMARY KEY, enabled INTEGER NOT NULL)",
		"CREATE TABLE executions(id TEXT PRIMARY KEY, kind TEXT NOT NULL, status TEXT NOT NULL)",
		"CREATE TABLE agent_runs(id TEXT PRIMARY KEY, status TEXT NOT NULL)",
		"CREATE TABLE workflows(id TEXT PRIMARY KEY)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	New(Dependencies{DB: db}).RegisterRoutes(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("overview status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"0 / 5", "Recommended next step: Connect a model provider", "href=\"/dashboard/providers\">Continue</a>"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("empty overview missing %q: %s", want, rec.Body.String())
		}
	}
}

func TestProviderAndModelPagesExplainFirstTimeSetup(t *testing.T) {
	h := New(Dependencies{})
	for _, tc := range []struct {
		active string
		wants  []string
	}{
		{active: "providers", wants: []string{
			"Connect the AI service you want AgentMesh to use",
			"Create provider",
			"Save the API key",
			"New API key",
			"Save key",
			"Add and enable a model",
			"test the connection",
		}},
		{active: "models", wants: []string{
			"Tell AgentMesh which model to call",
			"Model ID is an internal unique ID",
			"API model name is sent to the provider",
			"Select provider",
		}},
	} {
		var body bytes.Buffer
		if err := h.tmpl.ExecuteTemplate(&body, "page", pageData{Active: tc.active}); err != nil {
			t.Fatalf("render %s page: %v", tc.active, err)
		}
		for _, want := range tc.wants {
			if !strings.Contains(body.String(), want) {
				t.Errorf("%s page missing setup guidance %q", tc.active, want)
			}
		}
	}
}

func TestAgentPageExplainsFirstTaskAndUsesWorkspaceSelection(t *testing.T) {
	h := New(Dependencies{})
	var body bytes.Buffer
	data := pageData{Active: "agents", ModelOptions: []modelOption{{ID: "model-1", Name: "Main model"}}, AgentRows: []agentView{{ID: "agent-1", Name: "Helper"}}, WorkspaceOptions: []workspaceOption{{ID: "workspace-1", Name: "Default workspace"}}}
	if err := h.tmpl.ExecuteTemplate(&body, "page", data); err != nil {
		t.Fatalf("render agents page: %v", err)
	}
	for _, want := range []string{
		"Create an Agent, then give it one small task",
		"unique ID, a name, the enabled Model ID",
		"Select workspace",
		"Default workspace",
		"Describe one small task for this Agent",
		"Select enabled model",
	} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("agents page missing first-task guidance %q", want)
		}
	}
	if strings.Contains(body.String(), `name="workspace_id" placeholder="Workspace ID"`) {
		t.Fatal("run form should select a known workspace instead of requiring users to copy an ID")
	}
}

func TestEditFormsUseCatalogSelectionsAndPreserveCurrentAssignments(t *testing.T) {
	h := New(Dependencies{})
	for _, tc := range []struct {
		active string
		data   pageData
		wants  []string
	}{
		{
			active: "agents",
			data: pageData{
				Active:          "agents",
				AgentRows:       []agentView{{ID: "agent-1", Name: "Helper", ModelID: "model-disabled"}},
				AllModelOptions: []modelOption{{ID: "model-disabled", Name: "Legacy model"}},
			},
			wants: []string{`<select name="model_id" aria-label="Model">`,
				`<option value="model-disabled" selected>Legacy model · model-disabled</option>`},
		},
		{
			active: "models",
			data: pageData{
				Active:             "models",
				ModelRows:          []modelView{{ID: "model-1", ProviderID: "provider-disabled", Source: "provider"}},
				AllProviderOptions: []providerOption{{ID: "provider-disabled", Name: "Legacy provider"}},
			},
			wants: []string{
				`<select name="provider_id" aria-label="Provider">`,
				`<option value="provider-disabled" selected>Legacy provider · provider-disabled</option>`},
		},
	} {
		var body bytes.Buffer
		if err := h.tmpl.ExecuteTemplate(&body, "page", tc.data); err != nil {
			t.Fatalf("render %s page: %v", tc.active, err)
		}
		for _, want := range tc.wants {
			if !strings.Contains(body.String(), want) {
				t.Errorf("%s edit form missing %q", tc.active, want)
			}
		}
	}
}

func TestProviderConnectionErrorsGiveActionableGuidance(t *testing.T) {
	h := New(Dependencies{})
	for _, tc := range []struct {
		query string
		want  string
	}{
		{query: "provider-test-model", want: "add or enable one of its models"},
		{query: "provider-test-timeout", want: "Check the provider URL and network access"},
		{query: "provider-test-failed", want: "Check the API key, provider URL, and API model name"},
	} {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/dashboard/providers?error="+tc.query, nil)
		h.render(ctx, pageData{Title: "Providers", Active: "providers"}, http.StatusOK)
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("error %q missing guidance %q: %s", tc.query, tc.want, rec.Body.String())
		}
	}
}

func TestModelAndAgentCreationUseCatalogSelections(t *testing.T) {
	h := New(Dependencies{})
	for _, tc := range []struct {
		active string
		data   pageData
		wants  []string
	}{
		{active: "models", data: pageData{Active: "models", ProviderOptions: []providerOption{{ID: "provider-1", Name: "Primary provider"}}}, wants: []string{`<select name="provider_id" required>`, "Primary provider"}},
		{active: "agents", data: pageData{Active: "agents", ModelOptions: []modelOption{{ID: "model-1", Name: "Main model"}}}, wants: []string{`<select name="model_id" required>`, "Main model"}},
	} {
		var body bytes.Buffer
		if err := h.tmpl.ExecuteTemplate(&body, "page", tc.data); err != nil {
			t.Fatalf("render %s page: %v", tc.active, err)
		}
		for _, want := range tc.wants {
			if !strings.Contains(body.String(), want) {
				t.Errorf("%s page missing selection %q", tc.active, want)
			}
		}
	}
}

// TestWorkflowQuickstartExplainsTheGeneratedStepsAndRequiresExistingResources verifies
// that the guided Workflow form exposes only configured model/workspace choices.
func TestWorkflowQuickstartExplainsTheGeneratedStepsAndRequiresExistingResources(t *testing.T) {
	h := New(Dependencies{})
	var body bytes.Buffer
	data := pageData{
		Active:           "workflows",
		ModelOptions:     []modelOption{{ID: "model-1", Name: "Main model"}},
		WorkspaceOptions: []workspaceOption{{ID: "workspace-1", Name: "Default workspace"}},
	}
	if err := h.tmpl.ExecuteTemplate(&body, "page", data); err != nil {
		t.Fatalf("render workflows page: %v", err)
	}
	for _, want := range []string{
		"Create a reusable Agent Workflow",
		"Start → one Agent task → End",
		"Select enabled model",
		"Select workspace",
		"Agent instruction",
		"action=\"/dashboard/workflows/quickstart\"",
		"Main model · model-1",
		"Default workspace",
		"Save starter Workflow",
	} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("Workflow quickstart missing %q", want)
		}
	}
}

func TestWorkflowQuickstartDisablesCreationWithoutModelOrWorkspace(t *testing.T) {
	h := New(Dependencies{})
	var body bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&body, "page", pageData{Active: "workflows"}); err != nil {
		t.Fatalf("render workflows page: %v", err)
	}
	for _, want := range []string{
		"<button disabled>Save starter Workflow</button>",
		"No enabled models are available",
		"No workspace is available",
	} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("empty Workflow quickstart missing %q", want)
		}
	}
}

// TestWorkflowQuickstartOffersWorkspaceCreationWhenNoWorkspaceExists keeps
// first-time users from being blocked by a missing prerequisite.
func TestWorkflowQuickstartOffersWorkspaceCreationWhenNoWorkspaceExists(t *testing.T) {
	h := New(Dependencies{})
	var body bytes.Buffer
	data := pageData{
		Active:       "workflows",
		ModelOptions: []modelOption{{ID: "model-1", Name: "Main model"}},
	}
	if err := h.tmpl.ExecuteTemplate(&body, "page", data); err != nil {
		t.Fatalf("render workflows page: %v", err)
	}
	for _, want := range []string{
		"No workspace is available yet",
		"action=\"/dashboard/workspaces/quickstart\"",
		"Workspace name",
		"Workspace slug",
		"Create workspace",
	} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("empty-workspace quickstart missing %q", want)
		}
	}
}

func TestNewTaskPageShowsComposerAndAdvancedPaths(t *testing.T) {
	h := New(Dependencies{})
	var body bytes.Buffer
	data := pageData{
		Active:           "new-task",
		AgentRows:        []agentView{{ID: "agent-1", Name: "Research assistant", ModelID: "model-1"}},
		WorkspaceOptions: []workspaceOption{{ID: "workspace-1", Name: "Default workspace"}},
	}
	if err := h.tmpl.ExecuteTemplate(&body, "page", data); err != nil {
		t.Fatalf("render new task page: %v", err)
	}
	for _, want := range []string{
		"What would you like AgentMesh to do?",
		"action=\"/dashboard/new-task/agent\"",
		"Research assistant · model-1",
		"Default workspace",
		"Describe the task",
		"Start Agent task",
		"href=\"/dashboard/coding\"",
		"href=\"/dashboard/workflows\"",
	} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("new task page missing %q", want)
		}
	}
}

func TestNewTaskPageExplainsMissingPrerequisites(t *testing.T) {
	h := New(Dependencies{})
	var body bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&body, "page", pageData{Active: "new-task"}); err != nil {
		t.Fatalf("render empty new task page: %v", err)
	}
	for _, want := range []string{
		"Set up an Agent first",
		"href=\"/dashboard/agents\"",
		"href=\"/dashboard/providers\"",
		"href=\"/dashboard/models\"",
	} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("new task empty state missing %q", want)
		}
	}
}

func TestNewTaskRouteRejectsCrossOriginSubmission(t *testing.T) {
	router := gin.New()
	New(Dependencies{}).RegisterRoutes(router)
	req := httptest.NewRequest(http.MethodPost, "/dashboard/new-task/agent", strings.NewReader("agent_id=a&workspace_id=w&prompt=hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://attacker.example")
	req.Host = "agentmesh.local"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin submission status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestNewTaskRouteRequiresAgentAndWorkspaceServices(t *testing.T) {
	router := gin.New()
	New(Dependencies{}).RegisterRoutes(router)
	req := httptest.NewRequest(http.MethodPost, "/dashboard/new-task/agent", strings.NewReader("agent_id=a&workspace_id=w&prompt=hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://agentmesh.local")
	req.Host = "agentmesh.local"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing dependencies status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAgentRunDetailRendersSuccessAndFailureResults(t *testing.T) {
	h := New(Dependencies{})
	for _, tc := range []struct {
		name  string
		run   *agentRunDetailView
		wants []string
	}{
		{
			name: "success",
			run: &agentRunDetailView{
				ID: "run-success", DefinitionID: "agent-1", AgentName: "Research Agent",
				TaskID: "task-1", Status: "succeeded", Content: "Workspace summary",
				CreatedAt: "2026-11-21T12:00:00Z", StartedAt: "2026-11-21T12:00:01Z",
				FinishedAt: "2026-11-21T12:00:03Z", Duration: "2s", Steps: 2, ToolCalls: 1,
			},
			wants: []string{"AGENT RUN RESULT", "Research Agent", "succeeded", "Workspace summary", "2s", "task-1", "Start another task"},
		},
		{
			name: "failure",
			run: &agentRunDetailView{
				ID: "run-failed", DefinitionID: "agent-1", TaskID: "task-2",
				Status: "failed", Error: "agent reached maximum steps: limit=2",
				CreatedAt: "2026-11-21T12:00:00Z",
			},
			wants: []string{"failed", "Failure reason", "agent reached maximum steps: limit=2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			if err := h.tmpl.ExecuteTemplate(&body, "page", pageData{
				Title: "Agent Run " + tc.run.ID, Active: "agents", AgentRun: tc.run,
			}); err != nil {
				t.Fatalf("render Agent Run detail: %v", err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(body.String(), want) {
					t.Errorf("Agent Run detail missing %q: %s", want, body.String())
				}
			}
		})
	}
}

func TestAgentRunRouteRequiresAgentCatalog(t *testing.T) {
	router := gin.New()
	New(Dependencies{}).RegisterRoutes(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/agent-runs/run-1", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing Agent catalog status=%d body=%s", rec.Code, rec.Body.String())
	}
}
