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

func TestUsageEventsPaginationAndFilters(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil { t.Fatal(err) }
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE model_usage_events (
		id TEXT PRIMARY KEY, occurred_at TEXT NOT NULL, provider_id TEXT NOT NULL, model_id TEXT NOT NULL,
		input_tokens INTEGER, output_tokens INTEGER, total_tokens INTEGER, usage_source TEXT NOT NULL,
		currency TEXT, estimated_cost_micros INTEGER, execution_id TEXT, attempt_id TEXT, request_id TEXT, trace_id TEXT
	);
	INSERT INTO model_usage_events(id,occurred_at,provider_id,model_id,input_tokens,output_tokens,total_tokens,usage_source,execution_id)
	VALUES('u1','2026-11-15T10:00:00Z','openai','gpt-test',10,2,12,'provider_reported','exec-a'),
	      ('u2','2026-11-15T11:00:00Z','openai','gpt-test',20,3,23,'provider_reported','exec-a'),
	      ('u3','2026-11-15T12:00:00Z','other','model-x',NULL,NULL,NULL,'unavailable',NULL);`)
	if err != nil { t.Fatal(err) }
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewUsageHandler(db).RegisterRoutes(r.Group("/api/v1"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/events?since=2026-11-14T00:00:00Z&provider_id=openai&limit=1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String()) }
	if !strings.Contains(rec.Body.String(), `"has_more":true`) || !strings.Contains(rec.Body.String(), `"id":"u2"`) {
		t.Fatalf("unexpected first page: %s",rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/usage/events?since=2026-11-14T00:00:00Z&provider_id=openai&limit=1&offset=1", nil)
	rec = httptest.NewRecorder(); r.ServeHTTP(rec,req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"u1"`) || strings.Contains(rec.Body.String(), `"id":"u3"`) {
		t.Fatalf("unexpected second page: status=%d body=%s",rec.Code,rec.Body.String())
	}
}

func TestUsageEventsRejectsInvalidPagination(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil { t.Fatal(err) }
	defer db.Close()
	gin.SetMode(gin.TestMode)
	r := gin.New(); NewUsageHandler(db).RegisterRoutes(r.Group("/api/v1"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/v1/usage/events?limit=999",nil))
	if rec.Code != http.StatusBadRequest { t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String()) }
}
