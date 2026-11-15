package reflection

import (
	"errors"
	"math"
	"strings"
	"time"
)

var (
	ErrInvalidReflection = errors.New("invalid reflection")
	ErrNotFound          = errors.New("reflection not found")
)

type InsightKind string

const (
	InsightSuccessPattern InsightKind = "success_pattern"
	InsightFailureCause   InsightKind = "failure_cause"
	InsightImprovement    InsightKind = "improvement"
	InsightRisk           InsightKind = "risk"
)

type Evidence struct {
	NodeID string `json:"node_id"`
	Quote  string `json:"quote"`
}

type Insight struct {
	Kind           InsightKind `json:"kind"`
	Title          string      `json:"title"`
	Description    string      `json:"description"`
	Recommendation string      `json:"recommendation,omitempty"`
	Confidence     float64     `json:"confidence"`
	Evidence       []Evidence  `json:"evidence"`
}

type Reflection struct {
	ID           string    `json:"id"`
	GoalID       string    `json:"goal_id"`
	RunID        string    `json:"run_id"`
	EvaluationID string    `json:"evaluation_id"`
	ModelID      string    `json:"model_id"`
	Summary      string    `json:"summary"`
	Insights     []Insight `json:"insights"`
	CreatedAt    time.Time `json:"created_at"`
}

func (r Reflection) Validate() error {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.GoalID) == "" || strings.TrimSpace(r.RunID) == "" || strings.TrimSpace(r.EvaluationID) == "" || strings.TrimSpace(r.ModelID) == "" || strings.TrimSpace(r.Summary) == "" || len(r.Summary) > 8000 || len(r.Insights) > 32 {
		return ErrInvalidReflection
	}
	for _, in := range r.Insights {
		switch in.Kind {
		case InsightSuccessPattern, InsightFailureCause, InsightImprovement, InsightRisk:
		default:
			return ErrInvalidReflection
		}
		if strings.TrimSpace(in.Title) == "" || len(in.Title) > 200 || strings.TrimSpace(in.Description) == "" || len(in.Description) > 4000 || len(in.Recommendation) > 4000 || math.IsNaN(in.Confidence) || math.IsInf(in.Confidence, 0) || in.Confidence < 0 || in.Confidence > 1 || len(in.Evidence) == 0 || len(in.Evidence) > 16 {
			return ErrInvalidReflection
		}
		for _, ev := range in.Evidence {
			if strings.TrimSpace(ev.NodeID) == "" || strings.TrimSpace(ev.Quote) == "" || len(ev.Quote) > 4000 {
				return ErrInvalidReflection
			}
		}
	}
	return nil
}
