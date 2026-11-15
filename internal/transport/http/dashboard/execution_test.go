package dashboard

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

func TestDashboardExecutionDetailShowsAttemptsProvenanceAndTimeline(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	statements := []string{
		`CREATE TABLE executions (id TEXT PRIMARY KEY, kind TEXT, runtime TEXT, task_id TEXT, status TEXT, created_at INTEGER, updated_at INTEGER)`,
		`CREATE TABLE execution_attempts (id TEXT PRIMARY KEY, execution_id TEXT, attempt_number INTEGER, runtime TEXT, status TEXT, started_at INTEGER, finished_at INTEGER)`,
		`CREATE TABLE collaboration_artifact_versions (id TEXT, artifact_id TEXT, version INTEGER, media_type TEXT, content_sha256 TEXT, size_bytes INTEGER, source_execution_id TEXT, source_attempt_id TEXT)`,
		`CREATE TABLE collaboration_artifact_evidence (id TEXT, artifact_id TEXT, version_id TEXT, kind TEXT, statement TEXT, execution_id TEXT, attempt_id TEXT, created_at INTEGER)`,
		`CREATE TABLE execution_events (sequence INTEGER, event_id TEXT, event_key TEXT, event_type TEXT, actor_type TEXT, actor_id TEXT, source TEXT, payload_json TEXT, occurred_at INTEGER, execution_id TEXT, attempt_id TEXT)`,
		`CREATE TABLE execution_control_events (sequence INTEGER, event_id TEXT, event_key TEXT, event_type TEXT, actor_type TEXT, actor_id TEXT, source TEXT, payload_json TEXT, occurred_at INTEGER, execution_id TEXT)`,
		`INSERT INTO executions VALUES ('exec-1','agent','agent-runtime','task-1','succeeded',100,200)`,
		`INSERT INTO executions VALUES ('exec-coding','coding','coding-runtime','task-coding','running',400,500)`,
		`INSERT INTO executions VALUES ('exec-workflow','workflow','workflow-runtime','task-workflow','queued',400,500)`,
		`INSERT INTO executions VALUES ('exec-cli','cli','cli-runtime','task-cli','failed',400,500)`,
		`INSERT INTO execution_attempts VALUES ('attempt-1','exec-1',1,'agent-runtime','succeeded',100,200)`,
		`INSERT INTO collaboration_artifact_versions VALUES ('version-1','artifact-1',1,'text/plain','abc123',12,'exec-1','attempt-1')`,
		`INSERT INTO collaboration_artifact_evidence VALUES ('evidence-1','artifact-1','version-1','test','verified output','exec-1','attempt-1',200)`,
		`INSERT INTO execution_events VALUES (1,'event-1','key-1','attempt.succeeded','runtime','agent','agent','{}',300,'exec-1','attempt-1')`,
		`INSERT INTO execution_control_events VALUES (1,'control-1','key-control','cancel.requested','operator','operator-1','dashboard','{}',250,'exec-1')`,
	}
	for _, statement := range statements {
		if _, err = db.Exec(statement); err != nil {
			t.Fatalf("execute fixture: %v", err)
		}
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(Dependencies{DB: db}).RegisterRoutes(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/executions/exec-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"attempt-1", "artifact-1", "evidence-1", "attempt.succeeded", "cancel.requested"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("detail response missing %q: %s", want, rec.Body.String())
		}
	}
	if strings.Index(rec.Body.String(), "cancel.requested") > strings.Index(rec.Body.String(), "attempt.succeeded") {
		t.Fatal("timeline events are not ordered oldest first")
	}

	cursorRequest := httptest.NewRequest(http.MethodGet, "/dashboard/executions/exec-1?after_time_ns=250&after_stream=control&after_sequence=1", nil)
	cursorResponse := httptest.NewRecorder()
	r.ServeHTTP(cursorResponse, cursorRequest)
	if cursorResponse.Code != http.StatusOK {
		t.Fatalf("cursor detail status=%d body=%s", cursorResponse.Code, cursorResponse.Body.String())
	}
	if strings.Contains(cursorResponse.Body.String(), "cancel.requested") || !strings.Contains(cursorResponse.Body.String(), "attempt.succeeded") {
		t.Fatalf("timeline cursor did not advance correctly: %s", cursorResponse.Body.String())
	}

	for _, tc := range []struct{ kind, id string }{
		{kind: "agent", id: "exec-1"},
		{kind: "coding", id: "exec-coding"},
		{kind: "workflow", id: "exec-workflow"},
		{kind: "cli", id: "exec-cli"},
	} {
		listResponse := httptest.NewRecorder()
		r.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/dashboard/executions?kind="+tc.kind, nil))
		if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), tc.id) {
			t.Fatalf("kind filter %q did not show %q: status=%d body=%s", tc.kind, tc.id, listResponse.Code, listResponse.Body.String())
		}
	}
	taskResponse := httptest.NewRecorder()
	r.ServeHTTP(taskResponse, httptest.NewRequest(http.MethodGet, "/dashboard/executions?task=task-coding", nil))
	if taskResponse.Code != http.StatusOK || !strings.Contains(taskResponse.Body.String(), "exec-coding") || strings.Contains(taskResponse.Body.String(), "exec-cli") {
		t.Fatalf("task filter did not isolate the expected execution: %s", taskResponse.Body.String())
	}
	pageOne := httptest.NewRecorder()
	r.ServeHTTP(pageOne, httptest.NewRequest(http.MethodGet, "/dashboard/executions?limit=1", nil))
	if pageOne.Code != http.StatusOK || !strings.Contains(pageOne.Body.String(), "Next") {
		t.Fatalf("expected next page control: %s", pageOne.Body.String())
	}
	pageTwo := httptest.NewRecorder()
	r.ServeHTTP(pageTwo, httptest.NewRequest(http.MethodGet, "/dashboard/executions?limit=1&offset=1", nil))
	if pageTwo.Code != http.StatusOK || !strings.Contains(pageTwo.Body.String(), "Previous") {
		t.Fatalf("expected previous page control: %s", pageTwo.Body.String())
	}
}
