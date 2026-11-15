package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/thkx/agentmesh/internal/config"
)

type SQLite struct {
	DB *sql.DB
}

func OpenSQLite(ctx context.Context, cfg config.DatabaseConfig) (*SQLite, error) {
	dsn, err := buildDSN(cfg.DSN, cfg.BusyTimeout)
	if err != nil {
		return nil, err
	}

	if err := ensureDatabaseDirectory(cfg.DSN); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := configureSQLite(ctx, db, cfg.BusyTimeout); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLite{DB: db}, nil
}

func buildDSN(raw string, busyTimeout time.Duration) (string, error) {
	if strings.HasPrefix(raw, "file:") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("parse sqlite DSN: %w", err)
		}
		q := u.Query()
		q.Set("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
		q.Add("_pragma", "foreign_keys(1)")
		q.Add("_pragma", "journal_mode(WAL)")
		u.RawQuery = q.Encode()
		return u.String(), nil
	}

	absolute, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve sqlite path: %w", err)
	}

	u := url.URL{
		Scheme: "file",
		Path:   absolute,
	}

	q := u.Query()
	q.Set("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	u.RawQuery = q.Encode()

	return u.String(), nil
}

func ensureDatabaseDirectory(dsn string) error {
	if strings.HasPrefix(dsn, "file:") {
		return nil
	}

	dir := filepath.Dir(dsn)
	if dir == "." || dir == "" {
		return nil
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	return nil
}

func (s *SQLite) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

func (s *SQLite) Ping(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("sqlite database is not initialized")
	}
	return s.DB.PingContext(ctx)
}

func BusyTimeoutMilliseconds(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10)
}

func configureSQLite(ctx context.Context, db *sql.DB, busyTimeout time.Duration) error {
	statements := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout.Milliseconds()),
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite (%s): %w", statement, err)
		}
	}

	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("verify sqlite foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("sqlite foreign_keys is not enabled")
	}

	return nil
}
