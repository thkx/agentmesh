package database

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func RunMigrations(db *sql.DB) error {
	source, err := iofs.New(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}
	defer source.Close()

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("create sqlite migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("create migration runner: %w", err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}

	_, dirty, versionErr := m.Version()
	if versionErr != nil && !errors.Is(versionErr, migrate.ErrNilVersion) {
		return fmt.Errorf("read migration version: %w", versionErr)
	}
	if dirty {
		return fmt.Errorf("database migration is dirty; manual recovery is required")
	}

	return nil
}

func MigrationFiles() ([]string, error) {
	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	return entries, nil
}
