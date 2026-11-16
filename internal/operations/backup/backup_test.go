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
	if _, err = db.Exec("CREATE TABLE sample(id INTEGER PRIMARY KEY,value TEXT); INSERT INTO sample(value) VALUES('preserved')"); err != nil {
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
}
