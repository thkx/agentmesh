package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httptransport "github.com/thkx/agentmesh/internal/transport/http"
)

func TestP5APITokenBoundaryProtectsOperationalEndpoints(t *testing.T) {
	token := strings.Repeat("p5-token-", 5)
	router := httptransport.NewRouter(httptransport.Dependencies{APIToken: token})

	protected := []string{
		"/metrics",
		"/api/v1/operations/health",
		"/api/v1/diagnostics/report",
		"/api/v1/diagnostics/bundle",
	}
	for _, path := range protected {
		t.Run("unauthorized "+path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
		})
		t.Run("authorized "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("valid bearer token rejected: %s", rec.Body.String())
			}
			if rec.Code >= http.StatusInternalServerError && path != "/api/v1/operations/health" {
				t.Fatalf("unexpected server error status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestP5RejectsMalformedBearerAuthorization(t *testing.T) {
	token := strings.Repeat("x", 40)
	router := httptransport.NewRouter(httptransport.Dependencies{APIToken: token})
	for _, value := range []string{"", token, "Basic " + token, "Bearer", "Bearer " + token + " extra"} {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if value != "" {
			req.Header.Set("Authorization", value)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q: status=%d want=%d", value, rec.Code, http.StatusUnauthorized)
		}
	}
}

func TestP5GracefulShutdownEndpointSignalsApplication(t *testing.T) {
	token := strings.Repeat("shutdown-token-", 3)
	requests := make(chan struct{}, 1)
	router := httptransport.NewRouter(httptransport.Dependencies{
		APIToken: token, RequestShutdown: requests,
	})

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/v1/operations/shutdown", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized shutdown status=%d want=%d", unauthorized.Code, http.StatusUnauthorized)
	}
	select {
	case <-requests:
		t.Fatal("unauthorized request triggered shutdown")
	default:
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/operations/shutdown", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("authorized shutdown status=%d want=%d body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	select {
	case <-requests:
	default:
		t.Fatal("authorized shutdown request did not signal application")
	}
}
