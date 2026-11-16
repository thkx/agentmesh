package diagnostics

import (
	"strings"
	"testing"
	"time"
)

func TestBuildBoundsAndFiltersReport(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	report := Build(Input{
		Version: "v1.2.3", StartedAt: now.Add(-90 * time.Second), DatabaseReady: true, WorkerCount: 2,
		Metrics:    map[string]int64{"http_requests": 12, "provider_api_key": 999, "model_requests": 3},
		RecentLogs: []string{"authorization: ***", "api_key=canary-secret", "request /private/home/user/file"},
	}, now)
	if report.UptimeSeconds != 90 {
		t.Fatalf("uptime=%d", report.UptimeSeconds)
	}
	if _, ok := report.Metrics["provider_api_key"]; ok {
		t.Fatal("unapproved metric leaked")
	}
	if len(report.RecentLogs) != 3 {
		t.Fatalf("logs=%d", len(report.RecentLogs))
	}
	joined := strings.Join(report.RecentLogs, " ")
	for _, secret := range []string{"abcdefghijklmnop", "canary-secret", "/private/home/user/file"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("sensitive value leaked: %q", secret)
		}
	}
}

func TestBuildCapsLogsAndSanitizesVersion(t *testing.T) {
	logs := make([]string, 150)
	for i := range logs {
		logs[i] = "line"
	}
	report := Build(Input{Version: "bad version", RecentLogs: logs}, time.Now())
	if report.Version != "unknown" {
		t.Fatalf("version=%q", report.Version)
	}
	if len(report.RecentLogs) != 100 {
		t.Fatalf("logs=%d", len(report.RecentLogs))
	}
}

func TestRedactURLCredentialsAndQuery(t *testing.T) {
	got := Redact("endpoint ***example.test/v1?token=secret")
	if strings.Contains(got, "user:pass") || strings.Contains(got, "token=secret") {
		t.Fatalf("URL not redacted: %s", got)
	}
}
