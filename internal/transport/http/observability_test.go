package http

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/observability"
)

func TestRequestCorrelationMiddlewarePropagatesAndLogsIDs(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(requestCorrelationMiddleware(logger))
	router.GET("/items/:id", func(c *gin.Context) {
		if RequestIDFromContext(c.Request.Context()) == "" || TraceIDFromContext(c.Request.Context()) == "" || modelgateway.RequestIDFromContext(c.Request.Context()) != "client-request-1" {
			t.Fatal("correlation IDs missing from request context")
		}
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/items/secret-value", nil)
	req.Header.Set("X-Request-ID", "client-request-1")
	req.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") != "client-request-1" || rec.Header().Get("X-Trace-ID") != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected correlation headers: %#v", rec.Header())
	}
	if !strings.Contains(logs.String(), "client-request-1") || !strings.Contains(logs.String(), "0123456789abcdef0123456789abcdef") {
		t.Fatalf("correlation IDs missing from structured log: %s", logs.String())
	}
	if strings.Contains(logs.String(), "secret-value") {
		t.Fatalf("concrete route parameter leaked into log: %s", logs.String())
	}
}

func TestRequestMetricsMiddlewareRecordsRegisteredRoute(t *testing.T) {
	metrics := observability.NewMetrics()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(requestMetricsMiddleware(metrics))
	router.GET("/items/:id", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items/actual-id", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	metricsRec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(metricsRec.Body.String(), "actual-id") || !strings.Contains(metricsRec.Body.String(), `route="/items/:id"`) {
		t.Fatalf("route metric should use registered route pattern only: %s", metricsRec.Body.String())
	}
}
