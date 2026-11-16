// Command agentmesh-backup performs explicit backup verification and offline restore staging.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/thkx/agentmesh/internal/config"
	"github.com/thkx/agentmesh/internal/operations/backup"
	"github.com/thkx/agentmesh/internal/platform/database"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	pass := []byte(os.Getenv("AGENTMESH_BACKUP_PASSPHRASE"))
	switch os.Args[1] {
	case "create":
		fs := flag.NewFlagSet("create", flag.ExitOnError)
		configPath := fs.String("config", "configs/config.yaml", "AgentMesh config")
		output := fs.String("output", "", "encrypted backup destination")
		version := fs.String("version", "development", "application version")
		_ = fs.Parse(os.Args[2:])
		if *output == "" || len(pass) < 12 {
			fatal("output and AGENTMESH_BACKUP_PASSPHRASE (at least 12 bytes) are required")
		}
		cfg, err := config.Load(*configPath)
		if err != nil {
			fatal(err.Error())
		}
		db, err := database.OpenSQLite(context.Background(), cfg.Database)
		if err != nil {
			fatal(err.Error())
		}
		defer db.Close()
		dbPath := cfg.Database.DSN
		if strings.HasPrefix(dbPath, "file:") {
			if u, e := url.Parse(dbPath); e == nil && u.Path != "" {
				dbPath = u.Path
			}
		}
		keyPath := filepath.Join(filepath.Dir(dbPath), "keys", "credential.key")
		var schema uint
		_ = db.DB.QueryRow("SELECT version FROM schema_migrations WHERE dirty = 0").Scan(&schema)
		if err = backup.Create(context.Background(), backup.CreateOptions{Database: db.DB, CredentialKeyPath: keyPath, ApplicationVersion: *version, SchemaVersion: schema}, pass, *output); err != nil {
			fatal(err.Error())
		}
		fmt.Println("Encrypted backup created:", *output)
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		archive := fs.String("archive", "", "backup archive")
		_ = fs.Parse(os.Args[2:])
		if *archive == "" || len(pass) < 12 {
			fatal("archive and AGENTMESH_BACKUP_PASSPHRASE (at least 12 bytes) are required")
		}
		manifest, err := backup.Verify(*archive, pass)
		if err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Backup verified: created=%s version=%s schema=%d\n", manifest.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), manifest.ApplicationVersion, manifest.SchemaVersion)
	case "apply-restore":
		fs:=flag.NewFlagSet("apply-restore",flag.ExitOnError)
		archive:=fs.String("archive","","encrypted backup archive")
		databasePath:=fs.String("database","","active SQLite database path")
		credentialKey:=fs.String("credential-key","","active credential encryption key path")
		backupDir:=fs.String("backup-dir","","directory for pre-restore safety copies")
		stopped:=fs.Bool("confirm-stopped",false,"confirm AgentMesh has been stopped")
		_=fs.Parse(os.Args[2:])
		if *archive==""||*databasePath==""||*credentialKey==""||*backupDir==""||len(pass)<12{fatal("archive, database, credential-key, backup-dir and AGENTMESH_BACKUP_PASSPHRASE are required")}
		if !*stopped{fatal("refusing restore apply without --confirm-stopped")}
		staging,err:=os.MkdirTemp("","agentmesh-restore-verified-*");if err!=nil{fatal(err.Error())}
		defer os.RemoveAll(staging)
		if _,err=backup.ExtractVerified(*archive,pass,staging);err!=nil{fatal(err.Error())}
		result,err:=backup.ApplyRestoreOffline(staging,*databasePath,*credentialKey,*backupDir,true);if err!=nil{fatal(err.Error())}
		fmt.Printf("Offline restore applied. Previous database: %s; previous credential key: %s\n",result.DatabaseBackup,result.CredentialKeyBackup)
	case "restore":
		fs := flag.NewFlagSet("restore", flag.ExitOnError)
		archive := fs.String("archive", "", "backup archive")
		destination := fs.String("destination", "", "new empty restore directory")
		_ = fs.Parse(os.Args[2:])
		if *archive == "" || *destination == "" || len(pass) < 12 {
			fatal("archive, destination and AGENTMESH_BACKUP_PASSPHRASE (at least 12 bytes) are required")
		}
		manifest, err := backup.ExtractVerified(*archive, pass, *destination)
		if err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Verified restore staged in %s (schema %d). Stop AgentMesh and follow the offline replacement procedure; no active database was modified.\n", *destination, manifest.SchemaVersion)
	default:
		usage()
		os.Exit(2)
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "Usage: agentmesh-backup <create|verify|restore|apply-restore> [flags]\nPassphrase is read only from AGENTMESH_BACKUP_PASSPHRASE and is never persisted.")
}
func fatal(message string) { fmt.Fprintln(os.Stderr, "agentmesh-backup:", message); os.Exit(1) }
