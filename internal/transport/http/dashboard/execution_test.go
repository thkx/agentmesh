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
		`CREATE TABLE execution_events (sequence INTEGER, event_type TEXT, actor_type TEXT, source TEXT, payload_json TEXT, occurred_at INTEGER, execution_id TEXT)`,
		`CREATE TABLE execution_control_events (sequence INTEGER, event_type TEXT, actor_type TEXT, source TEXT, payload_json TEXT, occurred_at INTEGER, execution_id TEXT)`,
		`INSERT INTO executions VALUES ('exec-1','agent','agent-runtime','task-1','succeeded',100,200)`,
		`INSERT INTO execution_attempts VALUES ('attempt-1','exec-1',1,'agent-runtime','succeeded',100,200)`,
		`INSERT INTO collaboration_artifact_versions VALUES ('version-1','artifact-1',1,'text/plain','abc123',12,'exec-1','attempt-1')`,
		`INSERT INTO collaboration_artifact_evidence VALUES ('evidence-1','artifact-1','version-1','test','verified output','exec-1','attempt-1',200)`,
		`INSERT INTO execution_events VALUES (1,'attempt.succeeded','runtime','agent','{}',300,'exec-1')`,
		`INSERT INTO execution_control_events VALUES (1,'cancel.requested','operator','dashboard','{}',250,'exec-1')`,
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
	if strings.Index(rec.Body.String(), "attempt.succeeded") > strings.Index(rec.Body.String(), "cancel.requested") {
		t.Fatal("timeline events are not ordered newest first")
	}
}
