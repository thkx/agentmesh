package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/domain/execution"
)

func fakeCLI(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is Unix-only")
	}
	p := filepath.Join(t.TempDir(), "fake-agent")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestExecuteAndCancel(t *testing.T) {
	workspace := t.TempDir()
	bin := fakeCLI(t, `echo '{"result":"completed"}'`)
	e := New(map[execution.Provider]Adapter{execution.ProviderCodex: commandAdapter{path: bin, kind: execution.ProviderCodex}}, workspace)
	outside := t.TempDir()
	if _, err := e.Execute(context.Background(), execution.Request{ID: "outside", Provider: execution.ProviderCodex, Prompt: "task", WorkspacePath: outside}); err == nil {
		t.Fatal("expected workspace outside configured root to be rejected")
	}
	link := filepath.Join(workspace, "external-link")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := e.Execute(context.Background(), execution.Request{ID: "symlink", Provider: execution.ProviderCodex, Prompt: "task", WorkspacePath: link}); err == nil {
			t.Fatal("expected symlink escape to be rejected")
		}
	}
	result, err := e.Execute(context.Background(), execution.Request{ID: "ok", Provider: execution.ProviderCodex, Prompt: "task", WorkspacePath: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "completed" || result.Status != execution.StatusSucceeded {
		t.Fatalf("unexpected result: %#v", result)
	}
	bin2 := fakeCLI(t, "sleep 5")
	e = New(map[execution.Provider]Adapter{execution.ProviderCodex: commandAdapter{path: bin2, kind: execution.ProviderCodex}}, workspace)
	done := make(chan error, 1)
	go func() {
		_, err := e.Execute(context.Background(), execution.Request{ID: "cancel-me", Provider: execution.ProviderCodex, Prompt: "task", WorkspacePath: workspace, Timeout: 10 * time.Second})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		e.mu.Lock()
		_, ok := e.active["cancel-me"]
		e.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("execution did not start")
		}
		time.Sleep(time.Millisecond * 10)
	}
	if err := e.Cancel(context.Background(), "cancel-me"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected canceled error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("execution did not cancel")
	}
}
