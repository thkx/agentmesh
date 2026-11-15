package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/memory"
	reflectiondom "github.com/thkx/agentmesh/internal/domain/reflection"
)

type reflectionFake struct{ item reflectiondom.Reflection }

func (f reflectionFake) Get(context.Context, string, string) (reflectiondom.Reflection, error) {
	return f.item, nil
}

type repoFake struct {
	items   map[string]dom.Memory
	touched []string
}

func newRepoFake() *repoFake { return &repoFake{items: map[string]dom.Memory{}} }
func (r *repoFake) IngestCandidates(_ context.Context, items []dom.Memory) ([]dom.Memory, error) {
	out := []dom.Memory{}
	for _, m := range items {
		key := m.SourceReflectionID + ":" + fmt.Sprint(m.SourceIndex)
		if old, ok := r.items[key]; ok {
			out = append(out, old)
		} else {
			r.items[key] = m
			out = append(out, m)
		}
	}
	return out, nil
}
func (r *repoFake) Get(_ context.Context, id string) (dom.Memory, error) {
	for _, m := range r.items {
		if m.ID == id {
			return m, nil
		}
	}
	return dom.Memory{}, dom.ErrNotFound
}
func (r *repoFake) ListByGoal(_ context.Context, g string, _ int) ([]dom.Memory, error) {
	out := []dom.Memory{}
	for _, m := range r.items {
		if m.GoalID == g {
			out = append(out, m)
		}
	}
	return out, nil
}
func (r *repoFake) Promote(_ context.Context, id string, typ dom.Type, scope dom.ScopeType, scopeID, reason string, now time.Time) (dom.Memory, error) {
	m, e := r.Get(context.Background(), id)
	if e != nil {
		return m, e
	}
	if m.Status != dom.StatusCandidate {
		return dom.Memory{}, dom.ErrInvalidState
	}
	m.Type = typ
	m.Status = dom.StatusActive
	m.ScopeType = scope
	m.ScopeID = scopeID
	m.PromotionReason = reason
	m.PromotedAt = now
	m.UpdatedAt = now
	r.items[m.SourceReflectionID+":"+fmt.Sprint(m.SourceIndex)] = m
	return m, nil
}
func (r *repoFake) Archive(_ context.Context, id string, now time.Time) (dom.Memory, error) {
	m, e := r.Get(context.Background(), id)
	if e != nil {
		return m, e
	}
	if m.Status != dom.StatusActive {
		return dom.Memory{}, dom.ErrInvalidState
	}
	m.Status = dom.StatusArchived
	m.ArchivedAt = now
	r.items[m.SourceReflectionID+":"+fmt.Sprint(m.SourceIndex)] = m
	return m, nil
}
func (r *repoFake) Reject(_ context.Context, id, reason string, now time.Time) (dom.Memory, error) {
	m, e := r.Get(context.Background(), id)
	if e != nil {
		return m, e
	}
	if m.Status != dom.StatusCandidate {
		return dom.Memory{}, dom.ErrInvalidState
	}
	m.Status = dom.StatusRejected
	m.RejectionReason = reason
	r.items[m.SourceReflectionID+":"+fmt.Sprint(m.SourceIndex)] = m
	return m, nil
}
func (r *repoFake) SearchActive(_ context.Context, goal string, agents []string, _ int) ([]dom.Memory, error) {
	out := []dom.Memory{}
	for _, m := range r.items {
		if m.Status != dom.StatusActive {
			continue
		}
		if m.ScopeType == dom.ScopeGlobal || (m.ScopeType == dom.ScopeGoal && m.ScopeID == goal) {
			out = append(out, m)
			continue
		}
		for _, a := range agents {
			if m.ScopeType == dom.ScopeAgent && m.ScopeID == a {
				out = append(out, m)
			}
		}
	}
	return out, nil
}
func (r *repoFake) Touch(_ context.Context, ids []string, now time.Time) error {
	r.touched = append(r.touched, ids...)
	return nil
}
func TestIngestPromoteRetrieveAndArchive(t *testing.T) {
	repo := newRepoFake()
	ref := reflectiondom.Reflection{ID: "ref1", GoalID: "g1", RunID: "run1", Insights: []reflectiondom.Insight{{Kind: reflectiondom.InsightImprovement, Title: "Retry transient API timeouts", Description: "Retry transient API timeouts with bounded backoff.", Recommendation: "Use bounded exponential backoff.", Confidence: 0.9, Evidence: []reflectiondom.Evidence{{NodeID: "n1", Quote: "request timed out"}}}}}
	svc, err := NewService(repo, reflectionFake{ref})
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.IngestReflection(context.Background(), "g1", IngestRequest{RunID: "run1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != dom.StatusCandidate {
		t.Fatalf("unexpected candidates: %#v", items)
	}
	pending, err := svc.Search(context.Background(), SearchRequest{Query: "API timeout", GoalID: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.Items) != 0 {
		t.Fatal("candidate memory must not be retrievable before promotion")
	}
	promoted, err := svc.Promote(context.Background(), items[0].ID, PromoteRequest{Type: dom.TypeProcedural, ScopeType: dom.ScopeGlobal, Reason: "Reviewed evidence and confirmed reusable."})
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Status != dom.StatusActive || promoted.Type != dom.TypeProcedural {
		t.Fatalf("unexpected promotion: %#v", promoted)
	}
	result, err := svc.Search(context.Background(), SearchRequest{Query: "API timeout backoff", GoalID: "g2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Context == "" {
		t.Fatalf("memory not retrieved: %#v", result)
	}
	if len(repo.touched) != 1 {
		t.Fatal("retrieval access was not recorded")
	}
	if _, err = svc.Archive(context.Background(), promoted.ID); err != nil {
		t.Fatal(err)
	}
	result, err = svc.Search(context.Background(), SearchRequest{Query: "API timeout", GoalID: "g2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 {
		t.Fatal("archived memory must not be retrieved")
	}
}
func TestPromotionRequiresExplicitReason(t *testing.T) {
	svc, _ := NewService(newRepoFake(), reflectionFake{})
	_, err := svc.Promote(context.Background(), "m", PromoteRequest{Type: dom.TypeSemantic, ScopeType: dom.ScopeGlobal})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid request, got %v", err)
	}
}
