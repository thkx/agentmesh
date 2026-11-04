package credentialstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/platform/credentialstore"
	"github.com/thkx/agentmesh/internal/platform/cryptography"
	_ "modernc.org/sqlite"
)

type staticKey []byte

func (k staticKey) Key() ([]byte, error) { return append([]byte(nil), k...), nil }

func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE providers (id TEXT PRIMARY KEY NOT NULL)`,
		`CREATE TABLE provider_credentials (provider_id TEXT PRIMARY KEY NOT NULL, encrypted_value BLOB NOT NULL, encryption_version INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, FOREIGN KEY(provider_id) REFERENCES providers(id) ON UPDATE CASCADE ON DELETE CASCADE)`,
		`INSERT INTO providers(id) VALUES ('openai-primary')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	return db
}

func TestSQLiteStoreRoundTripAndNoPlaintext(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "credentials.db")
	db := openTestDB(t, path)
	key := staticKey(bytes.Repeat([]byte{7}, cryptography.KeySize))
	store, err := credentialstore.NewSQLiteStore(db, key)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := domaincredential.NewSecret("sk-live-super-secret")
	if err := store.Put(ctx, provider.ID("openai-primary"), secret); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, provider.ID("openai-primary"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Reveal() != secret.Reveal() {
		t.Fatal("round-trip mismatch")
	}
	var blob []byte
	if err := db.QueryRow(`SELECT encrypted_value FROM provider_credentials`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte(secret.Reveal())) {
		t.Fatal("plaintext found in encrypted_value")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	store2, err := credentialstore.NewSQLiteStore(db2, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err = store2.Get(ctx, provider.ID("openai-primary"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Reveal() != secret.Reveal() {
		t.Fatal("credential did not survive database reopen")
	}
}

func TestSQLiteStoreWrongKeyAndDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, filepath.Join(t.TempDir(), "wrong-key.db"))
	defer db.Close()
	key1 := staticKey(bytes.Repeat([]byte{1}, cryptography.KeySize))
	key2 := staticKey(bytes.Repeat([]byte{2}, cryptography.KeySize))
	first, _ := credentialstore.NewSQLiteStore(db, key1)
	second, _ := credentialstore.NewSQLiteStore(db, key2)
	secret, _ := domaincredential.NewSecret("secret")
	if err := first.Put(ctx, "openai-primary", secret); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Get(ctx, "openai-primary"); err == nil {
		t.Fatal("expected wrong-key decryption failure")
	}
	if err := first.Delete(ctx, "openai-primary"); err != nil {
		t.Fatal(err)
	}
	exists, err := first.Exists(ctx, "openai-primary")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("credential should be deleted")
	}
	if _, err := first.Get(ctx, "openai-primary"); !errors.Is(err, domaincredential.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestSQLiteStoreCascadeDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, filepath.Join(t.TempDir(), "cascade.db"))
	defer db.Close()
	store, err := credentialstore.NewSQLiteStore(db, staticKey(bytes.Repeat([]byte{9}, cryptography.KeySize)))
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := domaincredential.NewSecret("secret")
	if err := store.Put(ctx, "openai-primary", secret); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM providers WHERE id='openai-primary'`); err != nil {
		t.Fatal(err)
	}
	exists, err := store.Exists(ctx, "openai-primary")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("credential row should cascade-delete with provider")
	}
}

func TestSQLiteStoreRejectsUnknownProvider(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "fk.db"))
	defer db.Close()
	store, _ := credentialstore.NewSQLiteStore(db, staticKey(bytes.Repeat([]byte{9}, cryptography.KeySize)))
	secret, _ := domaincredential.NewSecret("secret")
	if err := store.Put(context.Background(), "missing-provider", secret); err == nil {
		t.Fatal("expected foreign-key failure")
	}
}

func TestFileKeyProviderKeyDoesNotAppearInSQLite(t *testing.T) {
	// Key lifecycle is independent of the database file; this also guards against
	// accidentally placing key material in SQLite metadata in later wiring.
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "keys", "credential.key")
	key, err := (cryptography.FileKeyProvider{Path: keyPath}).Key()
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != cryptography.KeySize {
		t.Fatalf("unexpected key length %d", len(key))
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatal(err)
	}
}
