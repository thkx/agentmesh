package builtin

import (
	"context"
	"encoding/json"
	dom "github.com/thkx/agentmesh/internal/domain/tool"
	"github.com/thkx/agentmesh/internal/tools"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileToolsStayInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	r := tools.NewRegistry()
	if err := Register(r, Config{}); err != nil {
		t.Fatal(err)
	}
	e := tools.NewEngine(r, allowPolicy{}, nil)
	ctx := context.Background()
	args := json.RawMessage(`{"path":"note.txt","content":"hello"}`)
	if _, err := e.Invoke(ctx, "file.write", args, root, true); err != nil {
		t.Fatal(err)
	}
	got, err := e.Invoke(ctx, "file.read", json.RawMessage(`{"path":"note.txt"}`), root, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.(result).Content != "hello" {
		t.Fatalf("unexpected content: %#v", got)
	}
	if _, err = e.Invoke(ctx, "file.read", json.RawMessage(`{"path":"../escape"}`), root, false); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err = os.Stat(filepath.Join(root, "note.txt")); err != nil {
		t.Fatal(err)
	}
}

type allowPolicy struct{}

func (allowPolicy) Authorize(context.Context, dom.Spec, json.RawMessage, string) tools.Decision {
	return tools.Decision{Allowed: true}
}
func TestShellCommandAllowlist(t *testing.T) {
	r := tools.NewRegistry()
	if err := Register(r, Config{AllowedCommands: []string{"echo"}, CommandTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	e := tools.NewEngine(r, allowPolicy{}, nil)
	root := t.TempDir()
	if _, err := e.Invoke(context.Background(), "shell.exec", json.RawMessage(`{"command":"not-allowed"}`), root, true); err == nil {
		t.Fatal("expected allowlist rejection")
	}
}
