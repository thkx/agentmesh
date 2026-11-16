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

func TestUsageSummaryKeepsCurrenciesSeparate(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE model_usage_events (
		id TEXT PRIMARY KEY, provider_id TEXT NOT NULL, model_id TEXT NOT NULL,
		occurred_at TEXT NOT NULL, input_tokens INTEGER, output_tokens INTEGER,
		total_tokens INTEGER, usage_source TEXT NOT NULL, currency TEXT,
		estimated_cost_micros INTEGER
	);
	INSERT INTO model_usage_events(id,provider_id,model_id,occurred_at,input_tokens,output_tokens,total_tokens,usage_source,currency,estimated_cost_micros)
	VALUES
	('usd-1','openai','model-a','2026-11-15T10:00:00Z',100,20,120,'provider_reported','USD',100),
	('eur-1','openai','model-a','2026-11-15T11:00:00Z',200,30,230,'provider_reported','EUR',250),
	('unknown-1','other','model-b','2026-11-15T12:00:00Z',NULL,NULL,NULL,'unavailable',NULL,NULL);`)
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewUsageHandler(db).RegisterRoutes(router.Group("/api/v1"))

	summary := httptest.NewRecorder()
	router.ServeHTTP(summary, httptest.NewRequest(http.MethodGet, "/api/v1/usage/summary?since=2026-11-14T00:00:00Z", nil))
	if summary.Code != http.StatusOK {
		t.Fatalf("summary status=%d body=%s", summary.Code, summary.Body.String())
	}
	for _, want := range []string{`"requests":3`, `"input_tokens":300`, `"output_tokens":50`, `"unavailable_usage":1`, `"currency":"EUR","estimated_cost_micros":250`, `"currency":"USD","estimated_cost_micros":100`} {
		if !strings.Contains(summary.Body.String(), want) {
			t.Fatalf("summary missing %q: %s", want, summary.Body.String())
		}
	}
	if strings.Contains(summary.Body.String(), `"estimated_cost_micros":350`) {
		t.Fatalf("costs in different currencies must never be summed: %s", summary.Body.String())
	}

	byModel := httptest.NewRecorder()
	router.ServeHTTP(byModel, httptest.NewRequest(http.MethodGet, "/api/v1/usage/by-model?since=2026-11-14T00:00:00Z", nil))
	if byModel.Code != http.StatusOK {
		t.Fatalf("by-model status=%d body=%s", byModel.Code, byModel.Body.String())
	}
	if !strings.Contains(byModel.Body.String(), `"currency":"EUR"`) || !strings.Contains(byModel.Body.String(), `"currency":"USD"`) {
		t.Fatalf("model breakdown must retain currency dimensions: %s", byModel.Body.String())
	}
}
