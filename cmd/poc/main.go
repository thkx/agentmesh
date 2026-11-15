package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "modernc.org/sqlite"

	dbgen "github.com/thkx/agentmesh/internal/db"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := os.MkdirAll("data", 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	dbPath, err := filepath.Abs(filepath.Join("data", "agentmesh-poc.db"))
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}

	dsn := "file:" + filepath.ToSlash(dbPath) +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}

	driver, err := migratesqlite.WithInstance(sqlDB, &migratesqlite.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}
	migrationsPath, err := filepath.Abs("migrations")
	if err != nil {
		return fmt.Errorf("resolve migrations path: %w", err)
	}
	m, err := migrate.NewWithDatabaseInstance(
		"file://"+filepath.ToSlash(migrationsPath), "sqlite", driver,
	)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() {
		sourceErr, dbErr := m.Close()
		if sourceErr != nil {
			log.Printf("close migration source: %v", sourceErr)
		}
		if dbErr != nil {
			log.Printf("close migration database: %v", dbErr)
		}
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}

	q := dbgen.New(sqlDB)
	now := time.Now().UTC().UnixMilli()
	email := fmt.Sprintf("poc-%d@example.test", now)
	if err := q.CreateUser(ctx, dbgen.CreateUserParams{
		Email: email, DisplayName: "AgentMesh PoC", CreatedAt: now,
	}); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}

	users, err := q.ListUsers(ctx)
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	fmt.Printf("SQLite + migrate + sqlc OK; users=%d; db=%s\n", len(users), dbPath)
	for _, user := range users {
		fmt.Printf("- id=%d email=%s display_name=%s\n", user.ID, user.Email, user.DisplayName)
	}
	return nil
}
