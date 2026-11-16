// Package backup creates authenticated, encrypted SQLite backup archives.
package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"golang.org/x/crypto/scrypt"
)

var magic = []byte("AGENTMESH-BACKUP-1\n")
var ErrInvalidArchive = errors.New("invalid or corrupted AgentMesh backup")

type Manifest struct {
	FormatVersion       int       `json:"format_version"`
	CreatedAt           time.Time `json:"created_at"`
	ApplicationVersion  string    `json:"application_version"`
	SchemaVersion       uint      `json:"schema_version"`
	DatabaseSHA256      string    `json:"database_sha256"`
	CredentialKeySHA256 string    `json:"credential_key_sha256"`
}
type CreateOptions struct {
	Database           *sql.DB
	CredentialKeyPath  string
	ApplicationVersion string
	SchemaVersion      uint
}

func Create(ctx context.Context, opts CreateOptions, passphrase []byte, destination string) error {
	if opts.Database == nil || len(passphrase) < 12 || destination == "" {
		return errors.New("database, destination and a passphrase of at least 12 bytes are required")
	}
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(dir, ".agentmesh-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	snapshot := filepath.Join(tmpDir, "database.sqlite")
	if _, err = opts.Database.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return fmt.Errorf("create consistent sqlite snapshot: %w", err)
	}
	dbBytes, err := os.ReadFile(snapshot)
	if err != nil {
		return err
	}
	keyBytes, err := os.ReadFile(opts.CredentialKeyPath)
	if err != nil {
		return fmt.Errorf("read credential encryption key: %w", err)
	}
	if len(keyBytes) == 0 {
		return errors.New("credential encryption key is empty")
	}
	manifest := Manifest{FormatVersion: 1, CreatedAt: time.Now().UTC(), ApplicationVersion: opts.ApplicationVersion, SchemaVersion: opts.SchemaVersion, DatabaseSHA256: digest(dbBytes), CredentialKeySHA256: digest(keyBytes)}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for name, data := range map[string][]byte{"database.sqlite": dbBytes, "credential.key": keyBytes} {
		w, e := zw.Create(name)
		if e != nil {
			return e
		}
		if _, e = w.Write(data); e != nil {
			return e
		}
	}
	mw, e := zw.Create("manifest.json")
	if e != nil {
		return e
	}
	if e = json.NewEncoder(mw).Encode(manifest); e != nil {
		return e
	}
	if e = zw.Close(); e != nil {
		return e
	}
	encrypted, err := seal(archive.Bytes(), passphrase)
	if err != nil {
		return err
	}
	out := append(append([]byte{}, magic...), encrypted...)
	temp := destination + ".tmp"
	if err = os.WriteFile(temp, out, 0o600); err != nil {
		return err
	}
	if err = os.Rename(temp, destination); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// Verify decrypts and validates every archive member without modifying active files.
func Verify(path string, passphrase []byte) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	if !bytes.HasPrefix(raw, magic) {
		return Manifest{}, ErrInvalidArchive
	}
	plain, err := open(raw[len(magic):], passphrase)
	if err != nil {
		return Manifest{}, ErrInvalidArchive
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		return Manifest{}, ErrInvalidArchive
	}
	members := map[string][]byte{}
	for _, f := range zr.File {
		if f.Name != "database.sqlite" && f.Name != "credential.key" && f.Name != "manifest.json" {
			return Manifest{}, ErrInvalidArchive
		}
		r, e := f.Open()
		if e != nil {
			return Manifest{}, ErrInvalidArchive
		}
		b, e := io.ReadAll(io.LimitReader(r, 1<<30))
		_ = r.Close()
		if e != nil {
			return Manifest{}, ErrInvalidArchive
		}
		members[f.Name] = b
	}
	var m Manifest
	if json.Unmarshal(members["manifest.json"], &m) != nil || m.FormatVersion != 1 || len(members["database.sqlite"]) == 0 || len(members["credential.key"]) == 0 {
		return Manifest{}, ErrInvalidArchive
	}
	if digest(members["database.sqlite"]) != m.DatabaseSHA256 || digest(members["credential.key"]) != m.CredentialKeySHA256 {
		return Manifest{}, ErrInvalidArchive
	}
	if err := verifySQLiteBytes(members["database.sqlite"], m.SchemaVersion); err != nil {
		return Manifest{}, ErrInvalidArchive
	}
	return m, nil
}

// ExtractVerified writes verified payloads to a new directory. It never replaces
// an active database; the caller must run this only from an offline restore command.
func ExtractVerified(path string, passphrase []byte, destinationDir string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	if !bytes.HasPrefix(raw, magic) {
		return Manifest{}, ErrInvalidArchive
	}
	plain, err := open(raw[len(magic):], passphrase)
	if err != nil {
		return Manifest{}, ErrInvalidArchive
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		return Manifest{}, ErrInvalidArchive
	}
	members := map[string][]byte{}
	for _, f := range zr.File {
		if f.Name != "database.sqlite" && f.Name != "credential.key" && f.Name != "manifest.json" {
			return Manifest{}, ErrInvalidArchive
		}
		r, e := f.Open()
		if e != nil {
			return Manifest{}, ErrInvalidArchive
		}
		b, e := io.ReadAll(io.LimitReader(r, 1<<30))
		_ = r.Close()
		if e != nil {
			return Manifest{}, ErrInvalidArchive
		}
		members[f.Name] = b
	}
	var m Manifest
	if json.Unmarshal(members["manifest.json"], &m) != nil || m.FormatVersion != 1 || digest(members["database.sqlite"]) != m.DatabaseSHA256 || digest(members["credential.key"]) != m.CredentialKeySHA256 {
		return Manifest{}, ErrInvalidArchive
	}
	if err := verifySQLiteBytes(members["database.sqlite"], m.SchemaVersion); err != nil {
		return Manifest{}, ErrInvalidArchive
	}
	if err = os.MkdirAll(destinationDir, 0o700); err != nil {
		return Manifest{}, err
	}
	for name, data := range map[string][]byte{"database.sqlite": members["database.sqlite"], "credential.key": members["credential.key"], "manifest.json": members["manifest.json"]} {
		target := filepath.Join(destinationDir, name)
		if _, e := os.Stat(target); e == nil {
			return Manifest{}, fmt.Errorf("refusing to overwrite existing restore file %s", name)
		}
		if e := os.WriteFile(target, data, 0o600); e != nil {
			return Manifest{}, e
		}
	}
	return m, nil
}

type RestoreResult struct {
	DatabaseBackup      string
	CredentialKeyBackup string
}

// ApplyRestoreOffline replaces the database and credential key only after the operator
// confirms the service is stopped. It verifies the staged manifest and keeps rollback copies.
func ApplyRestoreOffline(stagingDir, databasePath, credentialKeyPath, backupDir string, confirmStopped bool) (RestoreResult, error) {
	var result RestoreResult
	if !confirmStopped {
		return result, errors.New("refusing restore apply without --confirm-stopped")
	}
	if stagingDir == "" || databasePath == "" || credentialKeyPath == "" || backupDir == "" {
		return result, errors.New("staging, database, credential key and backup paths are required")
	}
	manifestBytes, err := os.ReadFile(filepath.Join(stagingDir, "manifest.json"))
	if err != nil {
		return result, err
	}
	dbBytes, err := os.ReadFile(filepath.Join(stagingDir, "database.sqlite"))
	if err != nil {
		return result, err
	}
	keyBytes, err := os.ReadFile(filepath.Join(stagingDir, "credential.key"))
	if err != nil {
		return result, err
	}
	var manifest Manifest
	if json.Unmarshal(manifestBytes, &manifest) != nil || manifest.FormatVersion != 1 || digest(dbBytes) != manifest.DatabaseSHA256 || digest(keyBytes) != manifest.CredentialKeySHA256 {
		return result, ErrInvalidArchive
	}
	if err = verifySQLiteBytes(dbBytes, manifest.SchemaVersion); err != nil {
		return result, ErrInvalidArchive
	}
	dbTarget, err := filepath.Abs(databasePath)
	if err != nil {
		return result, err
	}
	keyTarget, err := filepath.Abs(credentialKeyPath)
	if err != nil {
		return result, err
	}
	for _, target := range []string{dbTarget, keyTarget} {
		info, e := os.Lstat(target)
		if e != nil {
			return result, e
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return result, fmt.Errorf("restore target %s must be a regular non-symlink file", filepath.Base(target))
		}
	}
	if err = os.MkdirAll(backupDir, 0o700); err != nil {
		return result, err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	result.DatabaseBackup = filepath.Join(backupDir, "database."+stamp+".sqlite.bak")
	result.CredentialKeyBackup = filepath.Join(backupDir, "credential."+stamp+".key.bak")
	if err = copyBackupFile(dbTarget, result.DatabaseBackup, 0o600); err != nil {
		return RestoreResult{}, err
	}
	if err = copyBackupFile(keyTarget, result.CredentialKeyBackup, 0o600); err != nil {
		return RestoreResult{}, err
	}
	dbTemp, err := writeRestoreTemp(dbTarget, dbBytes)
	if err != nil {
		return RestoreResult{}, err
	}
	defer os.Remove(dbTemp)
	keyTemp, err := writeRestoreTemp(keyTarget, keyBytes)
	if err != nil {
		return RestoreResult{}, err
	}
	defer os.Remove(keyTemp)
	if err = os.Rename(dbTemp, dbTarget); err != nil {
		return RestoreResult{}, err
	}
	if err = os.Rename(keyTemp, keyTarget); err != nil {
		rollbackTemp, e := writeRestoreTemp(dbTarget, readFileOrEmpty(result.DatabaseBackup))
		if e == nil {
			e = os.Rename(rollbackTemp, dbTarget)
			_ = os.Remove(rollbackTemp)
		}
		return RestoreResult{}, errors.Join(err, e)
	}
	return result, nil
}

func writeRestoreTemp(target string, data []byte) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(target), ".agentmesh-restore-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err = file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}
func readFileOrEmpty(path string) []byte { data, _ := os.ReadFile(path); return data }

func seal(plain, pass []byte) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key(pass, salt, 1<<15, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{}, salt...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plain, magic)
	return out, nil
}
func open(data, pass []byte) ([]byte, error) {
	if len(data) < 16+12+16 {
		return nil, ErrInvalidArchive
	}
	salt := data[:16]
	key, err := scrypt.Key(pass, salt, 1<<15, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := data[16 : 16+gcm.NonceSize()]
	return gcm.Open(nil, nonce, data[16+gcm.NonceSize():], magic)
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func verifySQLiteBytes(data []byte, expectedSchema uint) error {
	file, err := os.CreateTemp("", "agentmesh-verify-*.sqlite")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	var integrity string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("sqlite integrity check failed")
	}
	if expectedSchema > 0 {
		var version uint
		var dirty bool
		if err = db.QueryRow("SELECT version, dirty FROM schema_migrations LIMIT 1").Scan(&version, &dirty); err != nil {
			return err
		}
		if dirty || version != expectedSchema {
			return fmt.Errorf("sqlite schema version mismatch")
		}
	}
	return nil
}

func copyBackupFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if syncErr != nil {
		_ = os.Remove(destination)
		return syncErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return closeErr
	}
	return nil
}
