package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestEncryptedBackupVerifyAndExtract(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE sample(id INTEGER PRIMARY KEY,value TEXT); CREATE TABLE schema_migrations(version INTEGER,dirty BOOLEAN); INSERT INTO schema_migrations(version,dirty) VALUES(44,0); INSERT INTO sample(value) VALUES('preserved')"); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "credential.key")
	if err = os.WriteFile(key, []byte("0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "agentmesh.backup")
	if err = Create(context.Background(), CreateOptions{Database: db, CredentialKeyPath: key, ApplicationVersion: "test", SchemaVersion: 44}, []byte("a-long-test-passphrase"), archive); err != nil {
		t.Fatal(err)
	}
	manifest, err := Verify(archive, []byte("a-long-test-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 44 {
		t.Fatalf("schema version=%d", manifest.SchemaVersion)
	}
	if _, err = Verify(archive, []byte("wrong-passphrase")); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	out := filepath.Join(t.TempDir(), "restore")
	if _, err = ExtractVerified(archive, []byte("a-long-test-passphrase"), out); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", filepath.Join(out, "database.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err = restored.QueryRow("SELECT value FROM sample").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "preserved" {
		t.Fatalf("restored value=%q", value)
	}
	currentDB := filepath.Join(t.TempDir(), "current.sqlite")
	currentKey := filepath.Join(t.TempDir(), "credential.key")
	if err = os.WriteFile(currentDB, []byte("old database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(currentKey, []byte("old credential key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyRestoreOffline(out, currentDB, currentKey, filepath.Join(t.TempDir(), "safety-backups"), false); err == nil {
		t.Fatal("restore apply without stop confirmation succeeded")
	}
	result, err := ApplyRestoreOffline(out, currentDB, currentKey, filepath.Join(t.TempDir(), "safety-backups"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(result.DatabaseBackup); err != nil {
		t.Fatalf("database rollback backup missing: %v", err)
	}
	if _, err = os.Stat(result.CredentialKeyBackup); err != nil {
		t.Fatalf("credential rollback backup missing: %v", err)
	}
	gotDB, err := os.ReadFile(currentDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotDB) == 0 {
		t.Fatal("restored database is empty")
	}
	gotKey, err := os.ReadFile(currentKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotKey) != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("restored credential key=%q", gotKey)
	}
}

func TestListAndPruneOnlyVerifiedArchives(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE schema_migrations(version INTEGER,dirty BOOLEAN); INSERT INTO schema_migrations(version,dirty) VALUES(44,0); CREATE TABLE sample(value TEXT);"); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "credential.key")
	if err = os.WriteFile(key, []byte("0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	passphrase := []byte("a-long-test-passphrase")
	for _, name := range []string{"one.backup", "two.backup", "three.backup"} {
		if err = Create(context.Background(), CreateOptions{Database: db, CredentialKeyPath: key, ApplicationVersion: "test", SchemaVersion: 44}, passphrase, filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	items, err := List(directory, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("listed %d backups, want 3", len(items))
	}
	corrupt := filepath.Join(directory, "corrupt.backup")
	if err = os.WriteFile(corrupt, []byte("not an encrypted archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Prune(directory, passphrase, 1); err == nil {
		t.Fatal("prune ignored an unverifiable archive")
	}
	if err = os.Remove(corrupt); err != nil {
		t.Fatal(err)
	}
	removed, err := Prune(directory, passphrase, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d backups, want 2", len(removed))
	}
	items, err = List(directory, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("retained %d backups, want 1", len(items))
	}
	if _, err = Prune(directory, passphrase, 0); err == nil {
		t.Fatal("prune accepted zero retention")
	}
}
