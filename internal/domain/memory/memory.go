package memory

import (
	"errors"
	"math"
	"strings"
	"time"
)

var (
	ErrInvalidMemory = errors.New("invalid memory")
	ErrNotFound      = errors.New("memory not found")
	ErrInvalidState  = errors.New("invalid memory lifecycle transition")
)

type Type string

const (
	TypeEpisodic   Type = "episodic"
	TypeSemantic   Type = "semantic"
	TypeProcedural Type = "procedural"
)

type Status string

const (
	StatusCandidate Status = "candidate"
	StatusActive    Status = "active"
	StatusArchived  Status = "archived"
	StatusRejected  Status = "rejected"
)

type ScopeType string

const (
	ScopeGlobal ScopeType = "global"
	ScopeGoal   ScopeType = "goal"
	ScopeAgent  ScopeType = "agent"
)

type Evidence struct {
	NodeID string `json:"node_id"`
	Quote  string `json:"quote"`
}

type Memory struct {
	ID                 string     `json:"id"`
	Type               Type       `json:"type"`
	Status             Status     `json:"status"`
	ScopeType          ScopeType  `json:"scope_type"`
	ScopeID            string     `json:"scope_id,omitempty"`
	GoalID             string     `json:"goal_id"`
	SourceReflectionID string     `json:"source_reflection_id"`
	SourceRunID        string     `json:"source_run_id"`
	SourceIndex        int        `json:"source_index"`
	Title              string     `json:"title"`
	Content            string     `json:"content"`
	Recommendation     string     `json:"recommendation,omitempty"`
	Confidence         float64    `json:"confidence"`
	Evidence           []Evidence `json:"evidence"`
	Tags               []string   `json:"tags,omitempty"`
	PromotionReason    string     `json:"promotion_reason,omitempty"`
	RejectionReason    string     `json:"rejection_reason,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	PromotedAt         time.Time  `json:"promoted_at,omitempty"`
	ArchivedAt         time.Time  `json:"archived_at,omitempty"`
	LastAccessedAt     time.Time  `json:"last_accessed_at,omitempty"`
	AccessCount        int64      `json:"access_count"`
}

func (m Memory) Validate() error {
	if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.GoalID) == "" || strings.TrimSpace(m.SourceReflectionID) == "" || strings.TrimSpace(m.SourceRunID) == "" || m.SourceIndex < 0 || strings.TrimSpace(m.Title) == "" || len(m.Title) > 200 || strings.TrimSpace(m.Content) == "" || len(m.Content) > 8000 || len(m.Recommendation) > 4000 || math.IsNaN(m.Confidence) || math.IsInf(m.Confidence, 0) || m.Confidence < 0 || m.Confidence > 1 || len(m.Evidence) > 16 || len(m.Tags) > 32 {
		return ErrInvalidMemory
	}
	if m.Type != TypeEpisodic && m.Type != TypeSemantic && m.Type != TypeProcedural {
		return ErrInvalidMemory
	}
	if m.Status != StatusCandidate && m.Status != StatusActive && m.Status != StatusArchived && m.Status != StatusRejected {
		return ErrInvalidMemory
	}
	if m.ScopeType != ScopeGlobal && m.ScopeType != ScopeGoal && m.ScopeType != ScopeAgent {
		return ErrInvalidMemory
	}
	if (m.ScopeType == ScopeGlobal && m.ScopeID != "") || (m.ScopeType != ScopeGlobal && strings.TrimSpace(m.ScopeID) == "") || (m.ScopeType == ScopeGoal && m.ScopeID != m.GoalID) {
		return ErrInvalidMemory
	}
	for _, e := range m.Evidence {
		if strings.TrimSpace(e.NodeID) == "" || strings.TrimSpace(e.Quote) == "" || len(e.Quote) > 4000 {
			return ErrInvalidMemory
		}
	}
	return nil
}
