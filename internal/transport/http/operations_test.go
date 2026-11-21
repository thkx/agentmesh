package http_test

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thkx/agentmesh/internal/observability"
	httptransport "github.com/thkx/agentmesh/internal/transport/http"
	_ "modernc.org/sqlite"
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

func TestDiagnosticsReportIncludesSQLiteHealthWithoutExposingPaths(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE schema_migrations(version INTEGER,dirty BOOLEAN); INSERT INTO schema_migrations(version,dirty) VALUES(44,0); CREATE TABLE sample(value TEXT);"); err != nil {
		t.Fatal(err)
	}
	router := httptransport.NewRouter(httptransport.Dependencies{DB: db})
	report := httptest.NewRecorder()
	router.ServeHTTP(report, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/report", nil))
	if report.Code != http.StatusOK {
		t.Fatalf("report status=%d body=%s", report.Code, report.Body.String())
	}
	var body map[string]any
	if err = json.Unmarshal(report.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["database_ready"] != true || body["database_integrity"] != "ok" || body["schema_version"] != float64(44) || body["migration_dirty"] != false {
		t.Fatalf("unexpected SQLite diagnostics: %#v", body)
	}
	if _, exists := body["database_path"]; exists {
		t.Fatalf("database path must not be exposed: %#v", body)
	}

	bundle := httptest.NewRecorder()
	router.ServeHTTP(bundle, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/bundle", nil))
	reader, err := zip.NewReader(bytes.NewReader(bundle.Body.Bytes()), int64(bundle.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if file.Name != "health.json" {
			continue
		}
		data, readErr := file.Open()
		if readErr != nil {
			t.Fatal(readErr)
		}
		var health map[string]any
		decodeErr := json.NewDecoder(data).Decode(&health)
		_ = data.Close()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if health["database_integrity"] != "ok" || health["schema_version"] != float64(44) {
			t.Fatalf("support bundle missing SQLite health: %#v", health)
		}
		return
	}
	t.Fatal("support bundle missing health.json")
}
