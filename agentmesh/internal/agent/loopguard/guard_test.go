package loopguard

import "testing"

func TestGuardBlocksThirdIdenticalToolCall(t *testing.T) {
	g := New(3, 5)
	for i := 0; i < 2; i++ {
		if err := g.CheckToolCall(i, "x", map[string]int{"a": 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.CheckToolCall(2, "x", map[string]int{"a": 1}); err == nil {
		t.Fatal("expected repeat blocked")
	}
	if err := g.CheckStep(4); err == nil {
		t.Fatal("expected step limit")
	}
}
