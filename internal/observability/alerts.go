package observability

import (
	"sort"
	"sync"
	"time"
)

// AlertSeverity is deliberately a closed set so alert labels remain bounded.
type AlertSeverity string

const (
	AlertWarning  AlertSeverity = "warning"
	AlertCritical AlertSeverity = "critical"
)

// AlertSnapshot contains only operational facts. It must never contain secrets,
// prompts, arbitrary error strings, request IDs or user-controlled labels.
type AlertSnapshot struct {
	DatabaseAvailable       bool
	WorkerRunning           bool
	ProviderRequests        uint64
	ProviderFailures        uint64
	ReconciliationBacklog   int64
	ObservedAt              time.Time
}

// Alert is a stable, redacted operational alert.
type Alert struct {
	RuleID      string        `json:"rule_id"`
	Severity    AlertSeverity `json:"severity"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	FirstSeenAt time.Time     `json:"first_seen_at"`
	LastSeenAt  time.Time     `json:"last_seen_at"`
	Occurrences uint64        `json:"occurrences"`
	ResolvedAt  *time.Time    `json:"resolved_at,omitempty"`
}

type alertRule struct {
	id          string
	severity    AlertSeverity
	title       string
	description string
	matches     func(AlertSnapshot) bool
}

// AlertEvaluator evaluates a small fixed rule set and retains bounded in-memory
// alert history. It does not execute external actions or send notifications.
type AlertEvaluator struct {
	mu      sync.RWMutex
	active  map[string]Alert
	history []Alert
	limit   int
	rules   []alertRule
}

func NewAlertEvaluator(historyLimit int) *AlertEvaluator {
	if historyLimit < 1 {
		historyLimit = 100
	}
	return &AlertEvaluator{
		active: make(map[string]Alert),
		limit:  historyLimit,
		rules: []alertRule{
			{id: "database_unavailable", severity: AlertCritical, title: "Database unavailable", description: "The application cannot reach its SQLite database.", matches: func(s AlertSnapshot) bool { return !s.DatabaseAvailable }},
			{id: "worker_stopped", severity: AlertCritical, title: "Runtime worker stopped", description: "A required runtime worker is not running.", matches: func(s AlertSnapshot) bool { return !s.WorkerRunning }},
			{id: "provider_failures", severity: AlertWarning, title: "Repeated provider failures", description: "At least five provider requests were observed and the failure ratio is at least 50%.", matches: func(s AlertSnapshot) bool { return s.ProviderRequests >= 5 && s.ProviderFailures*2 >= s.ProviderRequests }},
			{id: "reconciliation_backlog", severity: AlertWarning, title: "Execution reconciliation backlog", description: "The execution reconciliation backlog exceeds the operational threshold.", matches: func(s AlertSnapshot) bool { return s.ReconciliationBacklog > 10 }},
		},
	}
}

// Evaluate updates alert state atomically. The returned slice contains active
// alerts sorted by fixed rule ID for stable rendering and tests.
func (e *AlertEvaluator) Evaluate(snapshot AlertSnapshot) []Alert {
	if e == nil {
		return nil
	}
	now := snapshot.ObservedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	seen := make(map[string]bool, len(e.rules))
	for _, rule := range e.rules {
		if !rule.matches(snapshot) {
			continue
		}
		seen[rule.id] = true
		alert, exists := e.active[rule.id]
		if !exists {
			alert = Alert{RuleID: rule.id, Severity: rule.severity, Title: rule.title, Description: rule.description, FirstSeenAt: now}
		}
		alert.LastSeenAt = now
		alert.Occurrences++
		alert.ResolvedAt = nil
		e.active[rule.id] = alert
	}
	for id, alert := range e.active {
		if seen[id] {
			continue
		}
		resolved := now
		alert.ResolvedAt = &resolved
		e.history = append(e.history, alert)
		delete(e.active, id)
	}
	if len(e.history) > e.limit {
		e.history = append([]Alert(nil), e.history[len(e.history)-e.limit:]...)
	}
	return e.activeLocked()
}

func (e *AlertEvaluator) Active() []Alert {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeLocked()
}

func (e *AlertEvaluator) History() []Alert {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Alert, len(e.history))
	copy(out, e.history)
	return out
}

func (e *AlertEvaluator) activeLocked() []Alert {
	out := make([]Alert, 0, len(e.active))
	for _, alert := range e.active {
		out = append(out, alert)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RuleID < out[j].RuleID })
	return out
}
