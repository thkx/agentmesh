package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardBrowserSessionDoesNotAuthorizeAPI(t *testing.T) {
	const token = "api-token-for-dashboard-test"
	handler := NewRouter(Dependencies{APIToken: token})

	login := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("token="+token))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	login.Header.Set("Origin", "http://127.0.0.1")
	login.Host = "127.0.0.1"
	login.RemoteAddr = "127.0.0.1:12345"
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusSeeOther {
		t.Fatalf("dashboard login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range loginResponse.Result().Cookies() {
		if cookie.Name == "agentmesh_dashboard" {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("dashboard login did not issue a browser session")
	}

	apiRequest := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	apiRequest.AddCookie(session)
	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusUnauthorized {
		t.Fatalf("Dashboard cookie unexpectedly authorized API request: status=%d body=%s", apiResponse.Code, apiResponse.Body.String())
	}
}

func TestWorkflowEditorAssetsAreServedFromWebDirectory(t *testing.T) {
	handler := NewRouter(Dependencies{})
	for _, tc := range []struct {
		path        string
		contentType string
		want        string
	}{
		{path: "/workflow-editor", contentType: "text/html", want: "AgentMesh Workflow Editor"},
		{path: "/workflow-editor/app.js", contentType: "javascript", want: "workflowId"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", tc.path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), tc.contentType) || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s did not serve the expected embedded asset", tc.path)
		}
	}
}
