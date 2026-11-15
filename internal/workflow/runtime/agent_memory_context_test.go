package runtime

import "testing"

func TestAgentRenderContextSelectsOnlyBoundProfileMemory(t *testing.T) {
	contextData := map[string]any{
		"ticket": "ISSUE-42",
		"_agentmesh_memory_by_profile": map[string]any{
			"developer": "developer memory",
			"reviewer":  "reviewer memory",
		},
	}
	developer := agentRenderContext(contextData, "developer")
	if developer["_agentmesh_memory_context"] != "developer memory" {
		t.Fatalf("developer memory missing: %#v", developer)
	}
	if _, ok := developer["_agentmesh_memory_by_profile"]; ok {
		t.Fatal("profile memory map must not leak into rendered Agent context")
	}
	if developer["ticket"] != "ISSUE-42" {
		t.Fatal("ordinary workflow input was lost")
	}
	reviewer := agentRenderContext(contextData, "reviewer")
	if reviewer["_agentmesh_memory_context"] != "reviewer memory" {
		t.Fatalf("reviewer memory missing: %#v", reviewer)
	}
}
