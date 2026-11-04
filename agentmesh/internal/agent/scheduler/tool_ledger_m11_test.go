package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"
)

// This models a third-party API that honors an Idempotency-Key header. The
// ledger records intent before the request and refuses an ambiguous replay.
func TestM11ThirdPartySideEffectIsNotDuplicatedAfterAmbiguousCrash(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE agent_tool_invocations(idempotency_key TEXT PRIMARY KEY,task_id TEXT NOT NULL,tool_call_id TEXT NOT NULL,tool_name TEXT NOT NULL,status TEXT NOT NULL,result_json TEXT,error_text TEXT,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := NewToolLedger(db)
	if err != nil {
		t.Fatal(err)
	}

	var sideEffects atomic.Int32
	providerKeys := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			http.Error(w, "missing idempotency key", http.StatusBadRequest)
			return
		}
		if !providerKeys[key] {
			providerKeys[key] = true
			sideEffects.Add(1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer server.Close()

	ctx := context.Background()
	const key = "workflow-run:node:tool-call-1"
	shouldInvoke, err := ledger.Begin(ctx, key, "task-1", "call-1", "external-payment")
	if err != nil || !shouldInvoke {
		t.Fatalf("first Begin: invoke=%v err=%v", shouldInvoke, err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	req.Header.Set("Idempotency-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("provider status=%d", resp.StatusCode)
	}
	// Simulate a process dying after the external side effect but before
	// ToolLedger.Complete. A new worker must not call the provider again.
	shouldInvoke, err = ledger.Begin(ctx, key, "task-1", "call-1", "external-payment")
	if shouldInvoke {
		t.Fatal("ambiguous started invocation was authorized for replay")
	}
	if !errors.Is(err, ErrIndeterminate) {
		t.Fatalf("expected ErrIndeterminate, got %v", err)
	}
	if sideEffects.Load() != 1 {
		t.Fatalf("external side effects=%d, want 1", sideEffects.Load())
	}

	// Independently verify the provider's idempotency contract: even if a
	// caller retries at HTTP level with the same key, it applies once.
	for i := 0; i < 2; i++ {
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
		req.Header.Set("Idempotency-Key", "provider-retry-key")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if sideEffects.Load() != 2 {
		t.Fatalf("provider did not deduplicate repeated idempotency key: effects=%d", sideEffects.Load())
	}
}
