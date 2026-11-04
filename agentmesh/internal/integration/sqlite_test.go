package integration_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	dbgen "github.com/thkx/agentmesh/internal/db"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/platform/database"
)

func TestSQLiteMigrationAndSQLCQueries(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if err := database.RunMigrations(sqlDB); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	if err := database.RunMigrations(sqlDB); err != nil {
		t.Fatalf("migration should be idempotent: %v", err)
	}

	// Verify transaction rollback does not persist writes.
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO users (email, display_name, created_at) VALUES (?, ?, ?)",
		"rollback@example.test", "Rollback", time.Now().UTC().UnixMilli(),
	); err != nil {
		_ = tx.Rollback()
		t.Fatalf("transaction insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var rollbackCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE email = ?", "rollback@example.test").Scan(&rollbackCount); err != nil {
		t.Fatal(err)
	}
	if rollbackCount != 0 {
		t.Fatalf("rollback persisted row; count=%d", rollbackCount)
	}

	q := dbgen.New(sqlDB)
	email := "integration@example.test"
	if err := q.CreateUser(ctx, dbgen.CreateUserParams{Email: email, DisplayName: "Before", CreatedAt: time.Now().UTC().UnixMilli()}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	user, err := q.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("get user by email: %v", err)
	}
	if user.DisplayName != "Before" {
		t.Fatalf("unexpected name: %q", user.DisplayName)
	}
	affected, err := q.UpdateUserDisplayName(ctx, dbgen.UpdateUserDisplayNameParams{DisplayName: "After", ID: user.ID})
	if err != nil {
		t.Fatalf("update user: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expected one updated row, got %d", affected)
	}
	updated, err := q.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("get updated user: %v", err)
	}
	if updated.DisplayName != "After" {
		t.Fatalf("update did not persist: %q", updated.DisplayName)
	}
}

func TestRuntimeRepositoryPersistsProviderAndModel(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "runtime.db")
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if err := database.RunMigrations(sqlDB); err != nil {
		t.Fatal(err)
	}
	repo, err := database.NewRuntimeRepository(sqlDB)
	if err != nil {
		t.Fatal(err)
	}

	p := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "OpenAI Primary", Kind: provider.KindCloud, BaseURL: "https://api.openai.com/v1", CredentialRef: "OPENAI_PRIMARY_API_KEY", Enabled: true}
	m := provider.Model{ID: "assistant-fast", ProviderID: p.ID, Name: "gpt-4.1-mini", ContextWindow: 128000, MaxOutputTokens: 16384, SupportsStreaming: true, Enabled: true}
	if err := repo.Sync(ctx, []provider.Provider{p}, []provider.Model{m}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	providers, err := repo.ListProviders(ctx)
	if err != nil {
		t.Fatalf("list providers: %v", err)
	}
	if len(providers) != 1 || providers[0].CredentialRef != "OPENAI_PRIMARY_API_KEY" {
		t.Fatalf("unexpected providers: %#v", providers)
	}
	models, err := repo.ListModels(ctx)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models) != 1 || models[0].ProviderID != p.ID || !models[0].SupportsStreaming {
		t.Fatalf("unexpected models: %#v", models)
	}

	// A provider cannot be removed while a model still references it.
	if err := repo.DeleteProvider(ctx, p.ID); err == nil {
		t.Fatal("expected foreign-key restriction when deleting referenced provider")
	}
	if err := repo.DeleteModel(ctx, m.ID); err != nil {
		t.Fatalf("delete model: %v", err)
	}
	if err := repo.DeleteProvider(ctx, p.ID); err != nil {
		t.Fatalf("delete provider: %v", err)
	}
}

func TestRuntimeRepositorySyncIsAtomic(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "atomic.db")
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if err := database.RunMigrations(sqlDB); err != nil {
		t.Fatal(err)
	}
	repo, err := database.NewRuntimeRepository(sqlDB)
	if err != nil {
		t.Fatal(err)
	}

	p := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "OpenAI", Kind: provider.KindCloud, Enabled: true}
	badModel := provider.Model{ID: "orphan-model", ProviderID: "missing-provider", Name: "model", Enabled: true}
	if err := repo.Sync(ctx, []provider.Provider{p}, []provider.Model{badModel}); err == nil {
		t.Fatal("expected foreign-key error")
	}
	providers, err := repo.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 0 {
		t.Fatalf("provider persisted despite failed transaction: %#v", providers)
	}
}
