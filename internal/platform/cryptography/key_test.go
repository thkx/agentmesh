package cryptography

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAESGCMRoundTripAndNonceUniqueness(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, KeySize)
	c, err := NewAESGCM(key)
	if err != nil {
		t.Fatal(err)
	}
	a, err := c.Encrypt([]byte("secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Encrypt([]byte("secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("expected unique nonce/ciphertext for repeated plaintext")
	}
	got, err := c.Decrypt(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "secret-value" {
		t.Fatalf("unexpected plaintext %q", got)
	}
}

func TestAESGCMRejectsWrongKeyAndTampering(t *testing.T) {
	c1, _ := NewAESGCM(bytes.Repeat([]byte{1}, KeySize))
	c2, _ := NewAESGCM(bytes.Repeat([]byte{2}, KeySize))
	blob, err := c1.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Decrypt(blob); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected authentication error, got %v", err)
	}
	blob[len(blob)-1] ^= 0xff
	if _, err := c1.Decrypt(blob); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected tamper error, got %v", err)
	}
}

func TestFileKeyProviderPersistsSecureKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "key.bin")
	p := FileKeyProvider{Path: path}
	first, err := p.Key()
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Key()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("key changed across calls")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", info.Mode().Perm())
	}
}

func TestFileKeyProviderRejectsBroadPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte{1}, KeySize), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileKeyProvider{Path: path}).Key(); err == nil {
		t.Fatal("expected permission error")
	}
}
