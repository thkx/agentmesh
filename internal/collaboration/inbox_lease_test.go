package collaboration_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/collaboration"
	"github.com/thkx/agentmesh/internal/config"
	"github.com/thkx/agentmesh/internal/conversation"
	"github.com/thkx/agentmesh/internal/platform/database"
)

func newInboxTestService(t *testing.T) (*collaboration.Service, *database.SQLite) {
	t.Helper()
	cfg := config.Defaults().Database
	cfg.DSN = filepath.Join(t.TempDir(), "inbox.db")
	db, err := database.OpenSQLite(context.Background(), cfg)
	if err != nil { t.Fatal(err) }
	if err = database.RunMigrations(db.DB); err != nil { db.Close(); t.Fatal(err) }
	repo, err := conversation.NewSQLiteRepository(db.DB)
	if err != nil { db.Close(); t.Fatal(err) }
	cs, err := conversation.NewService(repo)
	if err != nil { db.Close(); t.Fatal(err) }
	s, err := collaboration.NewService(db.DB, cs)
	if err != nil { db.Close(); t.Fatal(err) }
	return s, db
}

func TestInboxLeaseFencesConcurrentConsumers(t *testing.T) {
	ctx := context.Background()
	s, db := newInboxTestService(t); defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name:"Lease",Slug:"lease"}); if err != nil { t.Fatal(err) }
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Agent",Role:"worker"}); if err != nil { t.Fatal(err) }
	if _, err = s.ReceiveEvent(ctx,a.ID,collaboration.Event{ID:"evt-1",Type:"test.event",Payload:[]byte(`{"ok":true}`)}); err != nil { t.Fatal(err) }
	first, err := s.ClaimInbox(ctx,a.ID,collaboration.InboxLeaseOptions{Owner:"worker-a",Duration:time.Minute}); if err != nil { t.Fatal(err) }
	if _, err = s.ClaimInbox(ctx,a.ID,collaboration.InboxLeaseOptions{Owner:"worker-b",Duration:time.Minute}); !errors.Is(err,collaboration.ErrNotFound) { t.Fatalf("second consumer should find no claimable item, got %v",err) }
	if err = s.AckInboxLease(ctx,a.ID,first.EventID,"worker-b",first.LeaseToken,"handled"); !errors.Is(err,collaboration.ErrInboxLeaseLost) { t.Fatalf("wrong owner must be fenced, got %v",err) }
	if err = s.AckInboxLease(ctx,a.ID,first.EventID,"worker-a",first.LeaseToken,"handled"); err != nil { t.Fatal(err) }
	if _, err = s.ClaimInbox(ctx,a.ID,collaboration.InboxLeaseOptions{Owner:"worker-c",Duration:time.Minute}); !errors.Is(err,collaboration.ErrNotFound) { t.Fatalf("acked event must not be claimed, got %v",err) }
}

func TestInboxLeaseExpiryAndRetry(t *testing.T) {
	ctx := context.Background()
	s, db := newInboxTestService(t); defer db.Close()
	w, err := s.CreateWorkspace(ctx, collaboration.Workspace{Name:"Retry",Slug:"retry"}); if err != nil { t.Fatal(err) }
	a, err := s.CreateAgent(ctx, collaboration.AgentIdentity{WorkspaceID:w.ID,Name:"Agent",Role:"worker"}); if err != nil { t.Fatal(err) }
	if _, err = s.ReceiveEvent(ctx,a.ID,collaboration.Event{ID:"evt-2",Type:"test.event",Payload:[]byte(`{"ok":true}`)}); err != nil { t.Fatal(err) }
	lease, err := s.ClaimInbox(ctx,a.ID,collaboration.InboxLeaseOptions{Owner:"worker-a",Duration:time.Second}); if err != nil { t.Fatal(err) }
	if _, err = s.FailInboxLease(ctx,a.ID,lease.EventID,"worker-a",lease.LeaseToken,"temporary",3); err != nil { t.Fatal(err) }
	if _, err = s.ClaimInbox(ctx,a.ID,collaboration.InboxLeaseOptions{Owner:"worker-b",Duration:time.Minute}); !errors.Is(err,collaboration.ErrNotFound) { t.Fatalf("backoff must prevent immediate retry, got %v",err) }
	var next int64
	if err = db.DB.QueryRowContext(ctx,"SELECT next_attempt_at FROM collaboration_inbox WHERE event_id='evt-2'").Scan(&next); err != nil { t.Fatal(err) }
	if err = db.DB.QueryRowContext(ctx,"UPDATE collaboration_inbox SET next_attempt_at=0 WHERE event_id='evt-2'").Scan(); err == nil { t.Fatal("expected no-row update scan error") }
	retry, err := s.ClaimInbox(ctx,a.ID,collaboration.InboxLeaseOptions{Owner:"worker-b",Duration:time.Minute}); if err != nil { t.Fatal(err) }
	if retry.Attempts != 2 { t.Fatalf("expected second attempt, got %d",retry.Attempts) }
	if err = s.AckInboxLease(ctx,a.ID,retry.EventID,"worker-b",retry.LeaseToken,"ignored"); err != nil { t.Fatal(err) }
}
