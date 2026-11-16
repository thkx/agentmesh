// Package diagnostics builds bounded, redacted support reports.
package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	assignmentSecret = regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret|authorization)(\s*[:=]\s*)([^\s,;]+)`)
	bearerSecret     = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/-]+=*`)
)

// Input contains only explicitly selected operational metadata. Callers must
// not pass prompts, completions, request bodies, or raw provider errors.
type Input struct {
	Version       string
	StartedAt     time.Time
	DatabaseReady bool
	WorkerCount   int
	Metrics       map[string]int64
	RecentLogs    []string
}

// Report is safe to serialize and attach to a support request.
type Report struct {
	GeneratedAt   time.Time        `json:"generated_at"`
	Version       string           `json:"version"`
	UptimeSeconds int64            `json:"uptime_seconds"`
	DatabaseReady bool             `json:"database_ready"`
	WorkerCount   int              `json:"worker_count"`
	Metrics       map[string]int64 `json:"metrics"`
	RecentLogs    []string         `json:"recent_logs"`
	ReportID      string           `json:"report_id"`
}

// Build creates a bounded report. Metric names are allow-listed and log lines
// are scrubbed, truncated, and limited before they enter the report.
func Build(in Input, now time.Time) Report {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	uptime := int64(0)
	if !in.StartedAt.IsZero() && now.After(in.StartedAt) {
		uptime = int64(now.Sub(in.StartedAt).Seconds())
	}
	allowed := map[string]bool{"http_requests": true, "model_requests": true, "http_in_flight": true, "execution_backlog": true, "runtime_workers": true, "active_executions": true, "usage_write_failures": true}
	metrics := make(map[string]int64)
	for k, v := range in.Metrics {
		if allowed[k] {
			metrics[k] = v
		}
	}
	logs := make([]string, 0, min(len(in.RecentLogs), 100))
	start := len(in.RecentLogs) - 100
	if start < 0 {
		start = 0
	}
	for _, line := range in.RecentLogs[start:] {
		line = Redact(line)
		if len(line) > 2048 {
			line = line[:2048]
		}
		logs = append(logs, line)
	}
	seed := now.UTC().Format(time.RFC3339Nano) + in.Version
	sum := sha256.Sum256([]byte(seed))
	return Report{GeneratedAt: now.UTC(), Version: safeVersion(in.Version), UptimeSeconds: uptime, DatabaseReady: in.DatabaseReady, WorkerCount: max(0, in.WorkerCount), Metrics: metrics, RecentLogs: logs, ReportID: hex.EncodeToString(sum[:8])}
}

// Redact removes common secret forms, URL credentials/query strings, and
// absolute local paths. It intentionally errs on the side of hiding data.
func Redact(value string) string {
	value = bearerSecret.ReplaceAllString(value, "***")
	value = assignmentSecret.ReplaceAllString(value, "$1$2[REDACTED]")
	fields := strings.Fields(value)
	for i, field := range fields {
		if strings.Contains(field, "://") {
			if u, err := url.Parse(strings.Trim(field, ",;")); err == nil && u.Scheme != "" {
				u.User = nil
				u.RawQuery = ""
				u.Fragment = ""
				fields[i] = u.String()
			}
		}
		if filepath.IsAbs(field) {
			fields[i] = "[PATH]"
		}
	}
	return strings.Join(fields, " ")
}

func safeVersion(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 64 {
		return v[:64]
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._+-", r)) {
			return "unknown"
		}
	}
	if v == "" {
		return "unknown"
	}
	return v
}
