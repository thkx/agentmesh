package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
	for _, want := range []string{"AgentMesh", "Overview", "Providers", "Executions"} {
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
	last := byte('x')
	if tampered.Value[len(tampered.Value)-1] == last {
		last = 'y'
	}
	tampered.Value = tampered.Value[:len(tampered.Value)-1] + string(last)
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
	for _, path := range []string{"/dashboard/providers", "/dashboard/models", "/dashboard/agents", "/dashboard/workflows", "/dashboard/executions"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}
