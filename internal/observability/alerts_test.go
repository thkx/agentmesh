package observability

import (
	"testing"
	"time"
)

func TestAlertEvaluatorActivatesAndResolvesRules(t *testing.T) {
	evaluator := NewAlertEvaluator(2)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	active := evaluator.Evaluate(AlertSnapshot{
		DatabaseAvailable: false,
		WorkerRunning:     true,
		ObservedAt:        now,
	})
	if len(active) != 1 || active[0].RuleID != "database_unavailable" {
		t.Fatalf("unexpected active alerts: %#v", active)
	}
	active = evaluator.Evaluate(AlertSnapshot{
		DatabaseAvailable: true,
		WorkerRunning:     true,
		ObservedAt:        now.Add(time.Minute),
	})
	if len(active) != 0 {
		t.Fatalf("expected no active alerts: %#v", active)
	}
	history := evaluator.History()
	if len(history) != 1 || history[0].ResolvedAt == nil || history[0].Occurrences != 1 {
		t.Fatalf("unexpected alert history: %#v", history)
	}
}

func TestAlertEvaluatorUsesFixedRulesAndBoundsHistory(t *testing.T) {
	evaluator := NewAlertEvaluator(1)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	evaluator.Evaluate(AlertSnapshot{DatabaseAvailable: false, WorkerRunning: false, ObservedAt: now})
	evaluator.Evaluate(AlertSnapshot{DatabaseAvailable: true, WorkerRunning: true, ObservedAt: now.Add(time.Minute)})
	evaluator.Evaluate(AlertSnapshot{DatabaseAvailable: false, WorkerRunning: true, ObservedAt: now.Add(2 * time.Minute)})
	evaluator.Evaluate(AlertSnapshot{DatabaseAvailable: true, WorkerRunning: true, ObservedAt: now.Add(3 * time.Minute)})
	if got := len(evaluator.History()); got != 1 {
		t.Fatalf("history should be bounded to 1, got %d", got)
	}
}

func TestAlertEvaluatorProviderAndBacklogThresholds(t *testing.T) {
	evaluator := NewAlertEvaluator(10)
	active := evaluator.Evaluate(AlertSnapshot{
		DatabaseAvailable:     true,
		WorkerRunning:         true,
		ProviderRequests:      6,
		ProviderFailures:      3,
		ReconciliationBacklog: 11,
	})
	if len(active) != 2 || active[0].RuleID != "provider_failures" || active[1].RuleID != "reconciliation_backlog" {
		t.Fatalf("unexpected active alerts: %#v", active)
	}
}
