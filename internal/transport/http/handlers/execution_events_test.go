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

func TestExecutionEventsHandlerOrdersAndPaginatesBySequence(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`CREATE TABLE executions(id TEXT PRIMARY KEY)`,
		`CREATE TABLE execution_events(
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL,event_key TEXT NOT NULL,execution_id TEXT NOT NULL,
			attempt_id TEXT,event_type TEXT NOT NULL,actor_type TEXT NOT NULL,
			actor_id TEXT NOT NULL,source TEXT NOT NULL,payload_json TEXT NOT NULL,occurred_at INTEGER NOT NULL
		)`,
		`INSERT INTO executions(id) VALUES('exec-1')`,
		`INSERT INTO execution_events(event_id,event_key,execution_id,event_type,actor_type,actor_id,source,payload_json,occurred_at)
		 VALUES('e1','k1','exec-1','execution.created','system','','execution_store','{}',100)`,
		`INSERT INTO execution_events(event_id,event_key,execution_id,event_type,actor_type,actor_id,source,payload_json,occurred_at)
		 VALUES('e2','k2','exec-1','execution.status_changed','system','','execution_store','{}',200)`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewExecutionEventsHandler(db).RegisterRoutes(router.Group(""))

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/managed-executions/exec-1/events?limit=1", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", first.Code, first.Body.String())
	}
	if got := first.Body.String(); !containsAll(got, `"event_id":"e1"`, `"next_sequence":1`, `"has_more":true`) {
		t.Fatalf("unexpected first page: %s", got)
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/managed-executions/exec-1/events?limit=1&after_sequence=1", nil))
	if second.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", second.Code, second.Body.String())
	}
	if got := second.Body.String(); !containsAll(got, `"event_id":"e2"`, `"next_sequence":2`, `"has_more":false`) {
		t.Fatalf("unexpected second page: %s", got)
	}
}

func TestExecutionEventsHandlerReturnsNotFoundForUnknownExecution(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE executions(id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE execution_events(
		sequence INTEGER PRIMARY KEY,event_id TEXT,event_key TEXT,execution_id TEXT,attempt_id TEXT,
		event_type TEXT,actor_type TEXT,actor_id TEXT,source TEXT,payload_json TEXT,occurred_at INTEGER
	)`); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewExecutionEventsHandler(db).RegisterRoutes(router.Group(""))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/managed-executions/missing/events", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
