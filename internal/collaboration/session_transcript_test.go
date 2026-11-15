package collaboration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thkx/agentmesh/internal/collaboration"
)

func TestAgentSessionTranscriptIsDurableIdempotentAndAppendOnly(t *testing.T) {
	ctx := context.Background()
	service, db := newInboxTestService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Transcript", Slug: "transcript"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Transcript Agent"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.EnsureAgentSession(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}

	input := collaboration.AgentSessionTranscriptEntry{
		SessionID: session.ID, AgentID: agent.ID, ExecutionID: "exec-1", TaskID: "task-1",
		EntryType: "execution_input", Content: "Implement the first task.",
	}
	first, err := service.AppendAgentSessionTranscript(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.AppendAgentSessionTranscript(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != retry.ID {
		t.Fatalf("idempotent retry created a second entry: first=%s retry=%s", first.ID, retry.ID)
	}

	result := input
	result.EntryType = "execution_result"
	result.Content = "The first task completed."
	if _, err = service.AppendAgentSessionTranscript(ctx, result); err != nil {
		t.Fatal(err)
	}

	items, err := service.ListAgentSessionTranscript(ctx, agent.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].EntryType != "execution_input" || items[1].EntryType != "execution_result" {
		t.Fatalf("expected chronological transcript entries, got %+v", items)
	}
	contextText, err := service.AgentSessionTranscriptContext(ctx, agent.ID, 10, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contextText, "Implement the first task.") || !strings.Contains(contextText, "The first task completed.") {
		t.Fatalf("session transcript context is incomplete: %q", contextText)
	}

	if _, err = db.DB.ExecContext(ctx, "UPDATE agent_session_transcript SET content='tampered' WHERE id=?", first.ID); err == nil {
		t.Fatal("expected append-only update trigger to reject mutation")
	}
	if _, err = db.DB.ExecContext(ctx, "DELETE FROM agent_session_transcript WHERE id=?", first.ID); err == nil {
		t.Fatal("expected append-only delete trigger to reject mutation")
	}
}

func TestAgentSessionTranscriptRejectsConflictingIdempotencyReplay(t *testing.T) {
	ctx := context.Background()
	service, db := newInboxTestService(t)
	defer db.Close()

	workspace, err := service.CreateWorkspace(ctx, collaboration.Workspace{Name: "Transcript Replay", Slug: "transcript-replay"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := service.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID: workspace.ID, Name: "Transcript Agent"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.EnsureAgentSession(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	entry := collaboration.AgentSessionTranscriptEntry{
		SessionID: session.ID, AgentID: agent.ID, ExecutionID: "exec-1",
		TaskID: "task-1", EntryType: "execution_result", Content: "result A",
	}
	if _, err = service.AppendAgentSessionTranscript(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Content = "result B"
	if _, err = service.AppendAgentSessionTranscript(ctx, entry); err != collaboration.ErrConflict {
		t.Fatalf("expected conflicting replay to fail, got %v", err)
	}
}
