// Package observability provides bounded-cardinality, local operational metrics.
package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var durationBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type requestKey struct {
	method string
	route  string
	status int
}

type modelRequestKey struct {
	operation string
	outcome   string
}

type modelRequestStats struct {
	count uint64
	sum   float64
}

type requestStats struct {
	count   uint64
	sum     float64
	buckets [len(durationBuckets)]uint64
}

// Metrics is an in-memory collector. It never stores request IDs, query strings,
// request bodies, model prompts, credentials, or arbitrary error messages.
type Metrics struct {
	started            time.Time
	inFlight           atomic.Int64
	mu                 sync.RWMutex
	requests           map[requestKey]*requestStats
	modelRequests      map[modelRequestKey]*modelRequestStats
	runtimeWorkers     atomic.Int64
	activeExecutions   atomic.Int64
	executionBacklog   atomic.Int64
	usageWriteFailures atomic.Uint64
	providerRequests     atomic.Uint64
	providerFailures   atomic.Uint64
}

func NewMetrics() *Metrics {
	return &Metrics{started: time.Now().UTC(), requests: make(map[requestKey]*requestStats), modelRequests: make(map[modelRequestKey]*modelRequestStats)}
}

func (m *Metrics) BeginRequest() {
	if m != nil {
		m.inFlight.Add(1)
	}
}

func (m *Metrics) EndRequest() {
	if m != nil {
		m.inFlight.Add(-1)
	}
}

func (m *Metrics) ObserveHTTPRequest(method, route string, status int, elapsed time.Duration) {
	if m == nil {
		return
	}
	method = normalizeMethod(method)
	route = normalizeRoute(route)
	if status < 100 || status > 599 {
		status = http.StatusInternalServerError
	}
	seconds := elapsed.Seconds()
	if seconds < 0 {
		seconds = 0
	}

	key := requestKey{method: method, route: route, status: status}
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := m.requests[key]
	if stats == nil {
		stats = &requestStats{}
		m.requests[key] = stats
	}
	stats.count++
	stats.sum += seconds
	for i, bound := range durationBuckets {
		if seconds <= bound {
			stats.buckets[i]++
		}
	}
}

func normalizeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

func normalizeRoute(route string) string {
	route = strings.TrimSpace(route)
	if route == "" || len(route) > 160 || !strings.HasPrefix(route, "/") {
		return "unmatched"
	}
	// Gin FullPath returns registered route patterns (for example /items/:id),
	// never concrete URL paths. Reject values that look like unregistered paths.
	if strings.Contains(route, "?") || strings.Contains(route, "\\") {
		return "unmatched"
	}
	return route
}

func (m *Metrics) AddRuntimeWorkers(delta int64) {
	if m != nil {
		m.runtimeWorkers.Add(delta)
	}
}
func (m *Metrics) SetExecutionState(active, backlog int64) {
	if m != nil {
		m.activeExecutions.Store(max(0, active))
		m.executionBacklog.Store(max(0, backlog))
	}
}
func (m *Metrics) ObserveUsageWriteFailure() {
	if m != nil {
		m.usageWriteFailures.Add(1)
	}
}

func (m *Metrics) ObserveModelGateway(operation, outcome string, elapsed time.Duration) {
	if m == nil {
		return
	}
	if operation != "generate" && operation != "stream_open" && operation != "stream" {
		operation = "other"
	}
	switch outcome {
	case "success", "error", "canceled":
	default:
		outcome = "error"
	}
	seconds := elapsed.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	if operation == "generate" || operation == "stream_open" {
		m.providerRequests.Add(1)
	}
	if outcome == "error" {
		m.providerFailures.Add(1)
	}
	key := modelRequestKey{operation: operation, outcome: outcome}
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := m.modelRequests[key]
	if stats == nil {
		stats = &modelRequestStats{}
		m.modelRequests[key] = stats
	}
	stats.count++
	stats.sum += seconds
}

type Snapshot struct {
	HTTPRequestCount     uint64
	ModelGatewayCount    uint64
	HTTPRequestsInFlight int64
	ProcessUptimeSeconds float64
	RuntimeWorkers       int64
	ActiveExecutions     int64
	ExecutionBacklog     int64
	ProviderRequests     uint64
	ProviderFailures     uint64
	UsageWriteFailures   uint64
}

func (m *Metrics) Snapshot() Snapshot {
	if m == nil {
		return Snapshot{}
	}
	result := Snapshot{
		HTTPRequestsInFlight: m.inFlight.Load(),
		ProcessUptimeSeconds: time.Since(m.started).Seconds(),
		RuntimeWorkers:       m.runtimeWorkers.Load(), ActiveExecutions: m.activeExecutions.Load(), ExecutionBacklog: m.executionBacklog.Load(), ProviderRequests: m.providerRequests.Load(), ProviderFailures: m.providerFailures.Load(), UsageWriteFailures: m.usageWriteFailures.Load(),
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, stats := range m.requests {
		result.HTTPRequestCount += stats.count
	}
	for _, stats := range m.modelRequests {
		result.ModelGatewayCount += stats.count
	}
	return result
}

func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if m == nil {
			_, _ = fmt.Fprintln(w, "# no metrics collector configured")
			return
		}

		m.mu.RLock()
		keys := make([]requestKey, 0, len(m.requests))
		snapshot := make(map[requestKey]requestStats, len(m.requests))
		for key, value := range m.requests {
			keys = append(keys, key)
			snapshot[key] = *value
		}
		m.mu.RUnlock()

		sort.Slice(keys, func(i, j int) bool {
			a, b := keys[i], keys[j]
			if a.route != b.route {
				return a.route < b.route
			}
			if a.method != b.method {
				return a.method < b.method
			}
			return a.status < b.status
		})

		_, _ = fmt.Fprintln(w, "# HELP agentmesh_http_requests_total Total HTTP requests handled.")
		_, _ = fmt.Fprintln(w, "# TYPE agentmesh_http_requests_total counter")
		_, _ = fmt.Fprintln(w, "# HELP agentmesh_http_request_duration_seconds HTTP request duration in seconds.")
		_, _ = fmt.Fprintln(w, "# TYPE agentmesh_http_request_duration_seconds histogram")
		for _, key := range keys {
			stats := snapshot[key]
			labels := fmt.Sprintf("method=%s,route=%s,status=%s", strconv.Quote(key.method), strconv.Quote(key.route), strconv.Quote(strconv.Itoa(key.status)))
			_, _ = fmt.Fprintf(w, "agentmesh_http_requests_total{%s} %d\n", labels, stats.count)
			for i, bound := range durationBuckets {
				le := strconv.FormatFloat(bound, 'f', -1, 64)
				_, _ = fmt.Fprintf(w, "agentmesh_http_request_duration_seconds_bucket{%s,le=%s} %d\n", labels, strconv.Quote(le), stats.buckets[i])
			}
			_, _ = fmt.Fprintf(w, "agentmesh_http_request_duration_seconds_bucket{%s,le=%s} %d\n", labels, strconv.Quote("+Inf"), stats.count)
			_, _ = fmt.Fprintf(w, "agentmesh_http_request_duration_seconds_sum{%s} %s\n", labels, strconv.FormatFloat(stats.sum, 'f', -1, 64))
			_, _ = fmt.Fprintf(w, "agentmesh_http_request_duration_seconds_count{%s} %d\n", labels, stats.count)
		}

		_, _ = fmt.Fprintln(w, "# HELP agentmesh_model_gateway_requests_total Model Gateway requests by operation and outcome.")
		_, _ = fmt.Fprintln(w, "# TYPE agentmesh_model_gateway_requests_total counter")
		_, _ = fmt.Fprintln(w, "# HELP agentmesh_model_gateway_request_duration_seconds Model Gateway operation setup duration in seconds.")
		_, _ = fmt.Fprintln(w, "# TYPE agentmesh_model_gateway_request_duration_seconds summary")
		m.mu.RLock()
		modelKeys := make([]modelRequestKey, 0, len(m.modelRequests))
		modelSnapshot := make(map[modelRequestKey]modelRequestStats, len(m.modelRequests))
		for key, value := range m.modelRequests {
			modelKeys = append(modelKeys, key)
			modelSnapshot[key] = *value
		}
		m.mu.RUnlock()
		sort.Slice(modelKeys, func(i, j int) bool {
			if modelKeys[i].operation != modelKeys[j].operation {
				return modelKeys[i].operation < modelKeys[j].operation
			}
			return modelKeys[i].outcome < modelKeys[j].outcome
		})
		for _, key := range modelKeys {
			stats := modelSnapshot[key]
			labels := fmt.Sprintf("operation=%s,outcome=%s", strconv.Quote(key.operation), strconv.Quote(key.outcome))
			_, _ = fmt.Fprintf(w, "agentmesh_model_gateway_requests_total{%s} %d\n", labels, stats.count)
			_, _ = fmt.Fprintf(w, "agentmesh_model_gateway_request_duration_seconds_sum{%s} %s\n", labels, strconv.FormatFloat(stats.sum, 'f', -1, 64))
			_, _ = fmt.Fprintf(w, "agentmesh_model_gateway_request_duration_seconds_count{%s} %d\n", labels, stats.count)
		}
		_, _ = fmt.Fprintln(w, "# HELP agentmesh_http_requests_in_flight Current number of HTTP requests being handled.")
		_, _ = fmt.Fprintln(w, "# TYPE agentmesh_http_requests_in_flight gauge")
		_, _ = fmt.Fprintf(w, "agentmesh_http_requests_in_flight %d\n", m.inFlight.Load())
		_, _ = fmt.Fprintln(w, "# HELP agentmesh_runtime_workers Current running runtime worker loops.")
		_, _ = fmt.Fprintln(w, "# TYPE agentmesh_runtime_workers gauge")
		_, _ = fmt.Fprintf(w, "agentmesh_runtime_workers %d\n", m.runtimeWorkers.Load())
		_, _ = fmt.Fprintln(w, "# HELP agentmesh_active_executions Current active executions.")
		_, _ = fmt.Fprintln(w, "# TYPE agentmesh_active_executions gauge")
		_, _ = fmt.Fprintf(w, "agentmesh_active_executions %d\n", m.activeExecutions.Load())
		_, _ = fmt.Fprintln(w, "# HELP agentmesh_execution_backlog Queued and reconciling executions.")
		_, _ = fmt.Fprintln(w, "# TYPE agentmesh_execution_backlog gauge")
		_, _ = fmt.Fprintf(w, "agentmesh_execution_backlog %d\n", m.executionBacklog.Load())
		_, _ = fmt.Fprintln(w, "# HELP agentmesh_usage_write_failures_total Failed durable usage writes.")
		_, _ = fmt.Fprintln(w, "# TYPE agentmesh_usage_write_failures_total counter")
		_, _ = fmt.Fprintf(w, "agentmesh_usage_write_failures_total %d\n", m.usageWriteFailures.Load())
		_, _ = fmt.Fprintln(w, "# HELP agentmesh_process_uptime_seconds Process uptime in seconds.")
		_, _ = fmt.Fprintln(w, "# TYPE agentmesh_process_uptime_seconds gauge")
		_, _ = fmt.Fprintf(w, "agentmesh_process_uptime_seconds %s\n", strconv.FormatFloat(time.Since(m.started).Seconds(), 'f', 3, 64))
	})
}
