package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/config"
)

func TestOpenSQLiteAndMigrate(t *testing.T) {
	ctx := context.Background()

	cfg := config.Defaults().Database
	cfg.DSN = filepath.Join(t.TempDir(), "agentmesh.db")
	cfg.BusyTimeout = 5 * time.Second

	db, err := OpenSQLite(ctx, cfg)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := RunMigrations(db.DB); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	var count int
	err = db.DB.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name = 'app_metadata'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query migrated table: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected app_metadata table, got count %d", count)
	}

	if err := RunMigrations(db.DB); err != nil {
		t.Fatalf("second migration run should be idempotent: %v", err)
	}
}

func TestSQLiteForeignKeysEnabled(t *testing.T) {
	cfg := config.Defaults().Database
	cfg.DSN = filepath.Join(t.TempDir(), "foreign-keys.db")

	db, err := OpenSQLite(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var enabled int
	if err := db.DB.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatalf("expected foreign_keys=1, got %d", enabled)
	}
}
