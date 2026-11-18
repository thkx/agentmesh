package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestShutdownClosesDatabaseAndAllowsReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentmesh.db")
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), "CREATE TABLE shutdown_probe (value TEXT NOT NULL); INSERT INTO shutdown_probe(value) VALUES ('persisted')"); err != nil {
		t.Fatal(err)
	}

	container := &Container{DB: db}
	if err := Shutdown(container, time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := db.Ping(); err == nil {
		t.Fatal("database remained open after shutdown")
	}

	reopened, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	var got string
	if err := reopened.QueryRow("SELECT value FROM shutdown_probe").Scan(&got); err != nil {
		t.Fatalf("query after reopen: %v", err)
	}
	if got != "persisted" {
		t.Fatalf("persisted value = %q, want persisted", got)
	}
}

func TestShutdownNilContainerIsSafe(t *testing.T) {
	if err := Shutdown(nil, time.Second); err != nil {
		t.Fatalf("shutdown nil container: %v", err)
	}
}

func TestContainerCloseNilIsSafe(t *testing.T) {
	var container *Container
	if err := container.Close(); err != nil {
		t.Fatalf("close nil container: %v", err)
	}
}
