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

func TestContainerCloseWaitsForWorkersBeforeClosingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentmesh.db")
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE worker_shutdown_probe (value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	workerObservedDB := make(chan error, 1)
	go func() {
		defer close(workerDone)
		<-workerCtx.Done()
		// A worker must be able to finish its final persistence before DB.Close.
		_, err := db.Exec("INSERT INTO worker_shutdown_probe(value) VALUES ('worker-drained')")
		workerObservedDB <- err
	}()

	container := &Container{
		DB:          db,
		workerCancel: cancelWorker,
		workerDone:   workerDone,
	}
	if err := container.Close(); err != nil {
		t.Fatalf("container close: %v", err)
	}
	if err := <-workerObservedDB; err != nil {
		t.Fatalf("worker could not persist its final state before database close: %v", err)
	}
	if err := db.Ping(); err == nil {
		t.Fatal("database remained open after container close")
	}

	reopened, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var got string
	if err := reopened.QueryRow("SELECT value FROM worker_shutdown_probe").Scan(&got); err != nil {
		t.Fatalf("read worker final state after reopen: %v", err)
	}
	if got != "worker-drained" {
		t.Fatalf("persisted worker state = %q, want worker-drained", got)
	}
}
