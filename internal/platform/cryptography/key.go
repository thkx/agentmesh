// Package cryptography contains platform-independent authenticated encryption primitives.
package cryptography

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

const KeySize = 32

var (
	ErrInvalidKey        = errors.New("encryption key must be exactly 32 bytes")
	ErrInvalidCiphertext = errors.New("invalid encrypted credential")
)

type KeyProvider interface{ Key() ([]byte, error) }

type AESGCM struct {
	aead   cipher.AEAD
	random io.Reader
}

func NewAESGCM(key []byte) (*AESGCM, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &AESGCM{aead: aead, random: rand.Reader}, nil
}

// Encrypt returns nonce || authenticated ciphertext. A fresh random nonce is generated per call.
func (c *AESGCM) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (c *AESGCM) Decrypt(blob []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(blob) < n+c.aead.Overhead() {
		return nil, ErrInvalidCiphertext
	}
	plaintext, err := c.aead.Open(nil, blob[:n], blob[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: authentication failed", ErrInvalidCiphertext)
	}
	return plaintext, nil
}
