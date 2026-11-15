package memory

import (
	"testing"
	"time"
)

func validMemory() Memory {
	now := time.Now().UTC()
	return Memory{ID: "m1", Type: TypeEpisodic, Status: StatusCandidate, ScopeType: ScopeGoal, ScopeID: "g1", GoalID: "g1", SourceReflectionID: "r1", SourceRunID: "run1", SourceIndex: 0, Title: "Retry after timeout", Content: "The API returned a timeout before the operation completed.", Confidence: 0.8, Evidence: []Evidence{{NodeID: "step-call", Quote: "request timed out"}}, CreatedAt: now, UpdatedAt: now}
}
func TestMemoryValidate(t *testing.T) {
	if err := validMemory().Validate(); err != nil {
		t.Fatal(err)
	}
	m := validMemory()
	m.Confidence = 1.1
	if err := m.Validate(); err == nil {
		t.Fatal("expected confidence validation error")
	}
	m = validMemory()
	m.ScopeType = ScopeGlobal
	m.ScopeID = "g1"
	if err := m.Validate(); err == nil {
		t.Fatal("expected global scope validation error")
	}
}
