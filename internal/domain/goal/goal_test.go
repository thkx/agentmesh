package goal

import "testing"

func TestGoalValidate(t *testing.T) {
	g := Goal{ID: "g1", Description: "research a product", Status: StatusActive}
	if err := g.Validate(); err != nil {
		t.Fatalf("valid goal rejected: %v", err)
	}
	g.Description = "  "
	if err := g.Validate(); err == nil {
		t.Fatal("expected empty description to fail")
	}
}
