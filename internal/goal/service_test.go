package goal

import (
	"context"
	"testing"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/goal"
)

type memoryRepository struct{ items map[string]dom.Goal }

func newMemoryRepository() *memoryRepository                           { return &memoryRepository{items: map[string]dom.Goal{}} }
func (r *memoryRepository) Create(_ context.Context, g dom.Goal) error { r.items[g.ID] = g; return nil }
func (r *memoryRepository) Update(_ context.Context, g dom.Goal) error { r.items[g.ID] = g; return nil }
func (r *memoryRepository) Get(_ context.Context, id string) (dom.Goal, error) {
	g, ok := r.items[id]
	if !ok {
		return dom.Goal{}, dom.ErrNotFound
	}
	return g, nil
}
func (r *memoryRepository) List(_ context.Context, _ int) ([]dom.Goal, error) {
	out := make([]dom.Goal, 0, len(r.items))
	for _, g := range r.items {
		out = append(out, g)
	}
	return out, nil
}
func (r *memoryRepository) Cancel(_ context.Context, id string, at time.Time) error {
	g, ok := r.items[id]
	if !ok {
		return dom.ErrNotFound
	}
	g.Status = dom.StatusCanceled
	g.UpdatedAt = at
	g.CanceledAt = &at
	r.items[id] = g
	return nil
}

func TestGoalServiceCreateUpdateCancel(t *testing.T) {
	repo := newMemoryRepository()
	service, err := NewService(repo)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), dom.Goal{Description: "Build a product", Constraints: []string{"solo developer"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != dom.StatusActive {
		t.Fatalf("unexpected created goal: %+v", created)
	}
	updated, err := service.Update(context.Background(), created.ID, dom.Goal{Description: "Build a cross-platform product"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != "Build a cross-platform product" || updated.CreatedAt != created.CreatedAt {
		t.Fatalf("unexpected updated goal: %+v", updated)
	}
	canceled, err := service.Cancel(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != dom.StatusCanceled {
		t.Fatalf("expected canceled, got %s", canceled.Status)
	}
	if _, err := service.Cancel(context.Background(), created.ID); err != nil {
		t.Fatalf("cancel should be idempotent: %v", err)
	}
	if _, err := service.Update(context.Background(), created.ID, dom.Goal{Description: "cannot update"}); err == nil {
		t.Fatal("expected canceled goal update to fail")
	}
}
