package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "modernc.org/sqlite"

	dbgen "github.com/thkx/agentmesh/internal/db"
)

func TestSQLiteMigrationAndSQLCQueries(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	dsn := "file:" + filepath.ToSlash(dbPath) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	driver, err := migratesqlite.WithInstance(sqlDB, &migratesqlite.Config{})
	if err != nil {
		t.Fatalf("migration driver: %v", err)
	}
	migrationsDir, err := filepath.Abs("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithDatabaseInstance(
		"file://"+filepath.ToSlash(migrationsDir), "sqlite", driver,
	)
	if err != nil {
		t.Fatalf("migrator: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migration up: %v", err)
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migration down: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migration re-up: %v", err)
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
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE email = ?", "rollback@example.test",
	).Scan(&rollbackCount); err != nil {
		t.Fatalf("count rolled-back row: %v", err)
	}
	if rollbackCount != 0 {
		t.Fatalf("rollback persisted row; count=%d", rollbackCount)
	}

	q := dbgen.New(sqlDB)
	email := "integration@example.test"
	if err := q.CreateUser(ctx, dbgen.CreateUserParams{
		Email: email, DisplayName: "Before", CreatedAt: time.Now().UTC().UnixMilli(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	user, err := q.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if user.DisplayName != "Before" {
		t.Fatalf("unexpected name: %q", user.DisplayName)
	}

	affected, err := q.UpdateUserDisplayName(ctx, dbgen.UpdateUserDisplayNameParams{
		DisplayName: "After", ID: user.ID,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expected one updated row, got %d", affected)
	}

	updated, err := q.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if updated.DisplayName != "After" {
		t.Fatalf("update did not persist: %q", updated.DisplayName)
	}
}
