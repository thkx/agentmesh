package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsHandlerExportsHTTPMeasurements(t *testing.T) {
	metrics := NewMetrics()
	metrics.BeginRequest()
	metrics.ObserveHTTPRequest("GET", "/api/v1/models", 200, 25*time.Millisecond)
	metrics.EndRequest()

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"agentmesh_http_requests_total{method=\"GET\",route=\"/api/v1/models\",status=\"200\"} 1",
		"agentmesh_http_request_duration_seconds_count{method=\"GET\",route=\"/api/v1/models\",status=\"200\"} 1",
		"agentmesh_http_requests_in_flight 0",
		"agentmesh_process_uptime_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output missing %q: %s", want, body)
		}
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("unexpected content type: %s", rec.Header().Get("Content-Type"))
	}
}

func TestMetricsNormalizesUnboundedLabels(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveHTTPRequest("CUSTOM-METHOD", strings.Repeat("x", 200), 200, time.Second)
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `method="OTHER",route="unmatched"`) {
		t.Fatalf("unbounded labels were not normalized: %s", body)
	}
}

func TestMetricsRecordsModelGatewayOperationsWithBoundedLabels(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveModelGateway("generate", "success", 125*time.Millisecond)
	metrics.ObserveModelGateway("stream_open", "canceled", 10*time.Millisecond)
	metrics.ObserveModelGateway("untrusted-operation", "untrusted-outcome", time.Second)

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`agentmesh_model_gateway_requests_total{operation="generate",outcome="success"} 1`,
		`agentmesh_model_gateway_requests_total{operation="stream_open",outcome="canceled"} 1`,
		`agentmesh_model_gateway_requests_total{operation="other",outcome="error"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output missing %q: %s", want, body)
		}
	}
}

func TestRuntimeAndLedgerMetricsAreExported(t *testing.T) {
	metrics := NewMetrics()
	metrics.AddRuntimeWorkers(3)
	metrics.SetExecutionState(4, 12)
	metrics.ObserveUsageWriteFailure()
	metrics.ObserveModelGateway("generate", "error", time.Second)
	snapshot := metrics.Snapshot()
	if snapshot.RuntimeWorkers != 3 || snapshot.ActiveExecutions != 4 || snapshot.ExecutionBacklog != 12 || snapshot.UsageWriteFailures != 1 || snapshot.ProviderFailures != 1 || snapshot.ProviderRequests != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	for _, want := range []string{"agentmesh_runtime_workers 3", "agentmesh_active_executions 4", "agentmesh_execution_backlog 12", "agentmesh_usage_write_failures_total 1"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("metrics output missing %q", want)
		}
	}
}

func TestSnapshotCountsEachStreamAsOneGatewayRequest(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveModelGateway("stream_open", "success", 5*time.Millisecond)
	metrics.ObserveModelGateway("stream", "success", 250*time.Millisecond)

	snapshot := metrics.Snapshot()
	if snapshot.ModelGatewayCount != 1 {
		t.Fatalf("stream open and completion must count as one request, got %d", snapshot.ModelGatewayCount)
	}
	if snapshot.ProviderRequests != 1 {
		t.Fatalf("provider request count=%d, want 1", snapshot.ProviderRequests)
	}
}
