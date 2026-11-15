package cryptography

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileKeyProvider is the portable fallback when an OS keychain integration is unavailable.
// The key is stored outside SQLite in a 0600 file under a 0700 directory.
type FileKeyProvider struct{ Path string }

func (p FileKeyProvider) Key() ([]byte, error) {
	if p.Path == "" {
		return nil, errors.New("encryption key file path is required")
	}
	dir := filepath.Dir(p.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat key directory: %w", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("encryption key directory must be private (0700 or stricter)")
	}

	key, err := readKey(p.Path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	generated := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, generated); err != nil {
		return nil, fmt.Errorf("generate encryption key: %w", err)
	}
	f, err := os.OpenFile(p.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return readKey(p.Path)
	}
	if err != nil {
		return nil, fmt.Errorf("create encryption key file: %w", err)
	}
	_, writeErr := f.Write(generated)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(p.Path)
		return nil, fmt.Errorf("write encryption key: %w", writeErr)
	}
	if syncErr != nil {
		return nil, fmt.Errorf("sync encryption key: %w", syncErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close encryption key: %w", closeErr)
	}
	return generated, nil
}

func readKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat encryption key file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("encryption key path is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("encryption key file permissions are too broad; require 0600")
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read encryption key: %w", err)
	}
	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}
	return key, nil
}
