package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

func unifiedExecutionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	statements := []string{
		`CREATE TABLE executions (
			id TEXT PRIMARY KEY, kind TEXT, runtime TEXT, runtime_ref TEXT, task_id TEXT,
			status TEXT, input_json TEXT, result_json TEXT, error_text TEXT,
			idempotency_key TEXT, created_at INTEGER, updated_at INTEGER, started_at INTEGER, finished_at INTEGER
		)`,
		`CREATE TABLE execution_attempts (
			id TEXT PRIMARY KEY, execution_id TEXT, attempt_number INTEGER, runtime TEXT,
			runtime_ref TEXT, status TEXT, input_json TEXT, result_json TEXT, error_text TEXT,
			started_at INTEGER, finished_at INTEGER
		)`,
		`CREATE TABLE execution_events (
			sequence INTEGER PRIMARY KEY, event_id TEXT, event_key TEXT, execution_id TEXT,
			attempt_id TEXT, event_type TEXT, actor_type TEXT, actor_id TEXT, source TEXT,
			payload_json TEXT, occurred_at INTEGER
		)`,
		`CREATE TABLE execution_control_events (
			sequence INTEGER PRIMARY KEY, event_id TEXT, event_key TEXT, execution_id TEXT,
			event_type TEXT, actor_type TEXT, actor_id TEXT, source TEXT, payload_json TEXT, occurred_at INTEGER
		)`,
		`CREATE TABLE collaboration_artifact_versions (
			id TEXT, artifact_id TEXT, version INTEGER, source_execution_id TEXT, source_attempt_id TEXT,
			content_sha256 TEXT, media_type TEXT, size_bytes INTEGER, created_at INTEGER
		)`,
		`CREATE TABLE collaboration_artifact_evidence (
			id TEXT, artifact_id TEXT, version_id TEXT, kind TEXT, source_ref TEXT, statement TEXT,
			execution_id TEXT, attempt_id TEXT, created_at INTEGER
		)`,
		`INSERT INTO executions VALUES
			('exec-1','agent','agent','runtime-1','task-1','succeeded','{"prompt":"x"}','{"ok":true}','',
			 'idem-1',1000000000,2000000000,1100000000,1900000000)`,
		`INSERT INTO execution_attempts VALUES
			('attempt-1','exec-1',1,'agent','runtime-1','succeeded','{"prompt":"x"}','{"ok":true}','',
			 1200000000,1800000000)`,
		`INSERT INTO execution_events VALUES
			(1,'evt-1','key-1','exec-1','attempt-1','execution.created','system','','execution_store','{"status":"queued"}',1300000000)`,
		`INSERT INTO execution_events VALUES
			(2,'evt-2','key-2','exec-1','attempt-1','execution.status_changed','system','','execution_store','{"to":"succeeded"}',1600000000)`,
		`INSERT INTO execution_control_events VALUES
			(1,'ctrl-1','control-1','exec-1','execution.cancel_requested','system','','execution_manager','{}',1500000000)`,
		`INSERT INTO collaboration_artifact_versions VALUES
			('version-1','artifact-1',1,'exec-1','attempt-1','abc123','text/plain',12,1700)`,
		`INSERT INTO collaboration_artifact_evidence VALUES
			('evidence-1','artifact-1','version-1','test','go test ./...','passed','exec-1','attempt-1',1750)`,
	}
	for _, statement := range statements {
		if _, err = db.Exec(statement); err != nil {
			t.Fatalf("setup database: %v", err)
		}
	}
	return db
}

func TestUnifiedExecutionTimelineMergesLifecycleAndControlStreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := unifiedExecutionDB(t)
	router := gin.New()
	NewUnifiedExecutionHandler(db).RegisterRoutes(router.Group(""))

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet,
		"/managed-executions/exec-1/timeline?limit=2", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", first.Code, first.Body.String())
	}
	body := first.Body.String()
	if !strings.Contains(body, `"event_id":"evt-1"`) ||
		!strings.Contains(body, `"event_id":"ctrl-1"`) ||
		!strings.Contains(body, `"has_more":true`) {
		t.Fatalf("timeline did not merge/order both streams: %s", body)
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet,
		"/managed-executions/exec-1/timeline?limit=2&after_time_ns=1500000000&after_stream=control&after_sequence=1", nil))
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"event_id":"evt-2"`) {
		t.Fatalf("second page status=%d body=%s", second.Code, second.Body.String())
	}
}

func TestUnifiedExecutionDetailIncludesAttemptsArtifactsAndEvidence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := unifiedExecutionDB(t)
	router := gin.New()
	NewUnifiedExecutionHandler(db).RegisterRoutes(router.Group(""))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/managed-executions/exec-1/detail", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, expected := range []string{
		`"runtime":"agent"`, `"id":"attempt-1"`, `"artifact_id":"artifact-1"`,
		`"source_attempt_id":"attempt-1"`, `"id":"evidence-1"`,
		`"execution_id":"exec-1"`, `"attempt_id":"attempt-1"`,
		`"input":{"prompt":"x"}`, `"result":{"ok":true}`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("detail missing %q: %s", expected, body)
		}
	}
}

func TestUnifiedExecutionViewsReturnNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := unifiedExecutionDB(t)
	router := gin.New()
	NewUnifiedExecutionHandler(db).RegisterRoutes(router.Group(""))
	for _, path := range []string{
		"/managed-executions/missing/detail",
		"/managed-executions/missing/timeline",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}
