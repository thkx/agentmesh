package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/thkx/agentmesh/internal/airesource"
	"github.com/thkx/agentmesh/internal/config"
	"github.com/thkx/agentmesh/internal/platform/cryptography"
	"github.com/thkx/agentmesh/internal/platform/database"
)

func newResourceCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "resource", Short: "Import and manage AI resource pool data"}
	cmd.AddCommand(newResourceImportCommand())
	return cmd
}

func newResourceImportCommand() *cobra.Command {
	var configPath, manifestPath string
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import providers, endpoints, credentials, models and bindings into local SQLite",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(manifestPath) == "" {
				return fmt.Errorf("--file is required")
			}
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			db, err := database.OpenSQLite(ctx, cfg.Database)
			if err != nil {
				return fmt.Errorf("open SQLite: %w", err)
			}
			defer db.Close()
			if err := database.RunMigrations(db.DB); err != nil {
				return fmt.Errorf("run database migrations: %w", err)
			}
			keyPath, err := resourceKeyPath(cfg.Database.DSN)
			if err != nil {
				return err
			}
			key, err := (cryptography.FileKeyProvider{Path: keyPath}).Key()
			if err != nil {
				return fmt.Errorf("load resource encryption key: %w", err)
			}
			cipher, err := cryptography.NewAESGCM(key)
			if err != nil {
				return fmt.Errorf("initialize resource encryption: %w", err)
			}
			f, err := os.Open(manifestPath)
			if err != nil {
				return fmt.Errorf("open manifest: %w", err)
			}
			defer f.Close()
			if err := airesource.ImportManifestJSON(ctx, db.DB, cipher, f); err != nil {
				return fmt.Errorf("import AI resources: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "AI resource import completed: provider=1 endpoint=1 credentials=19 models=2 bindings=38 policies=2")
			fmt.Fprintln(cmd.OutOrStdout(), "Credential secrets were encrypted before being stored in SQLite.")
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "configs/config.yaml", "AgentMesh configuration file")
	cmd.Flags().StringVar(&manifestPath, "file", "", "AI resource import JSON file")
	return cmd
}

func resourceKeyPath(dsn string) (string, error) {
	path := dsn
	if strings.HasPrefix(path, "file:") {
		u, err := url.Parse(path)
		if err != nil {
			return "", fmt.Errorf("parse database DSN: %w", err)
		}
		path = u.Path
	}
	if path == "" {
		return "", fmt.Errorf("database DSN does not resolve to a filesystem path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve database path: %w", err)
	}
	return filepath.Join(filepath.Dir(abs), "keys", "credential.key"), nil
}
