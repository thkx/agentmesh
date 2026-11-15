package goal

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidGoal       = errors.New("invalid goal")
	ErrNotFound          = errors.New("goal not found")
	ErrInvalidTransition = errors.New("invalid goal state transition")
)

type Status string

const (
	StatusActive   Status = "active"
	StatusCanceled Status = "canceled"
)

type SuccessCriterion struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type Goal struct {
	ID              string             `json:"id"`
	Description     string             `json:"description"`
	Constraints     []string           `json:"constraints"`
	SuccessCriteria []SuccessCriterion `json:"success_criteria"`
	Context         map[string]any     `json:"context,omitempty"`
	Status          Status             `json:"status"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	CanceledAt      *time.Time         `json:"canceled_at,omitempty"`
	CompletedAt     *time.Time         `json:"completed_at,omitempty"`
}

func (g Goal) Validate() error {
	if strings.TrimSpace(g.ID) == "" || strings.TrimSpace(g.Description) == "" || len(g.Description) > 20000 {
		return ErrInvalidGoal
	}
	if len(g.Constraints) > 64 || len(g.SuccessCriteria) > 64 {
		return ErrInvalidGoal
	}
	for _, constraint := range g.Constraints {
		if strings.TrimSpace(constraint) == "" || len(constraint) > 2000 {
			return ErrInvalidGoal
		}
	}
	seen := make(map[string]struct{}, len(g.SuccessCriteria))
	for _, criterion := range g.SuccessCriteria {
		if strings.TrimSpace(criterion.ID) == "" || strings.TrimSpace(criterion.Description) == "" || len(criterion.Description) > 2000 {
			return ErrInvalidGoal
		}
		if _, ok := seen[criterion.ID]; ok {
			return ErrInvalidGoal
		}
		seen[criterion.ID] = struct{}{}
	}
	if g.Context != nil {
		b, err := json.Marshal(g.Context)
		if err != nil || len(b) > 64*1024 {
			return ErrInvalidGoal
		}
	}
	if g.Status != "" && g.Status != StatusActive && g.Status != StatusCanceled && g.Status != StatusCompleted {
		return ErrInvalidGoal
	}
	return nil
}
