package reflection

import (
	"testing"
	"time"
)

func TestReflectionValidation(t *testing.T) {
	r := Reflection{ID: "r1", GoalID: "g1", RunID: "run1", EvaluationID: "eval1", ModelID: "m1", Summary: "Observed lessons", CreatedAt: time.Now(), Insights: []Insight{{Kind: InsightSuccessPattern, Title: "Small steps", Description: "Small verified steps produced reliable output", Confidence: .9, Evidence: []Evidence{{NodeID: "n1", Quote: "tests passed"}}}}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Insights[0].Confidence = 1.1
	if err := r.Validate(); err == nil {
		t.Fatal("expected invalid confidence")
	}
}

func TestReflectionAllowsNoInsightsWhenEvidenceIsInsufficient(t *testing.T) {
	r := Reflection{ID: "r1", GoalID: "g1", RunID: "run1", EvaluationID: "eval1", ModelID: "m1", Summary: "No reusable lesson was supported by the available evidence", CreatedAt: time.Now()}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}
