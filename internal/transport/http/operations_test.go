package http_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thkx/agentmesh/internal/observability"
	httptransport "github.com/thkx/agentmesh/internal/transport/http"
)

func TestOperationsHealthAlertsAndSupportBundle(t *testing.T) {
	metrics := observability.NewMetrics()
	metrics.AddRuntimeWorkers(2)
	router := httptransport.NewRouter(httptransport.Dependencies{Metrics: metrics})
	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v1/operations/health", nil))
	if health.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(health.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["worker_running"] != true || status["runtime_workers"] != float64(2) {
		t.Fatalf("unexpected health response: %#v", status)
	}

	alerts := httptest.NewRecorder()
	router.ServeHTTP(alerts, httptest.NewRequest(http.MethodGet, "/api/v1/operations/alerts", nil))
	if alerts.Code != http.StatusOK || !bytes.Contains(alerts.Body.Bytes(), []byte("database_unavailable")) {
		t.Fatalf("alerts status=%d body=%s", alerts.Code, alerts.Body.String())
	}

	bundle := httptest.NewRecorder()
	router.ServeHTTP(bundle, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/bundle", nil))
	if bundle.Code != http.StatusOK || bundle.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("bundle status=%d content-type=%s", bundle.Code, bundle.Header().Get("Content-Type"))
	}
	reader, err := zip.NewReader(bytes.NewReader(bundle.Body.Bytes()), int64(bundle.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	for _, name := range []string{"diagnostics.json", "metrics.prom", "health.json"} {
		if !names[name] {
			t.Fatalf("support bundle missing %s", name)
		}
	}
}

func TestOperationsShutdownRequiresConfiguredChannel(t *testing.T) {
	requests := make(chan struct{}, 1)
	router := httptransport.NewRouter(httptransport.Dependencies{RequestShutdown: requests})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/operations/shutdown", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-requests:
	default:
		t.Fatal("shutdown request was not delivered")
	}
}
