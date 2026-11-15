// Package credentialstore implements encrypted credential persistence on SQLite.
package credentialstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/db"
	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/platform/cryptography"
)

const encryptionVersion = 1

var ErrUnsupportedVersion = errors.New("unsupported credential encryption version")

type SQLiteStore struct {
	db      *sql.DB
	queries *db.Queries
	cipher  *cryptography.AESGCM
}

func NewSQLiteStore(sqlDB *sql.DB, keys cryptography.KeyProvider) (*SQLiteStore, error) {
	if sqlDB == nil || keys == nil {
		return nil, appcredential.ErrNilDependency
	}
	key, err := keys.Key()
	if err != nil {
		return nil, fmt.Errorf("load credential encryption key: %w", err)
	}
	cipher, err := cryptography.NewAESGCM(key)
	if err != nil {
		return nil, fmt.Errorf("initialize credential encryption: %w", err)
	}
	return &SQLiteStore{db: sqlDB, queries: db.New(sqlDB), cipher: cipher}, nil
}

func (s *SQLiteStore) Put(ctx context.Context, id provider.ID, secret domaincredential.Secret) error {
	if id == "" {
		return domaincredential.ErrInvalidProviderID
	}
	if secret.Empty() {
		return domaincredential.ErrEmptySecret
	}
	encrypted, err := s.cipher.Encrypt([]byte(secret.Reveal()))
	if err != nil {
		return fmt.Errorf("encrypt provider credential: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	err = s.queries.UpsertProviderCredential(ctx, db.UpsertProviderCredentialParams{
		ProviderID: string(id), EncryptedValue: encrypted, EncryptionVersion: encryptionVersion,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return fmt.Errorf("store provider credential: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Get(ctx context.Context, id provider.ID) (domaincredential.Secret, error) {
	row, err := s.queries.GetProviderCredential(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domaincredential.Secret{}, domaincredential.ErrNotFound
	}
	if err != nil {
		return domaincredential.Secret{}, fmt.Errorf("load provider credential: %w", err)
	}
	if row.EncryptionVersion != encryptionVersion {
		return domaincredential.Secret{}, fmt.Errorf("%w: %d", ErrUnsupportedVersion, row.EncryptionVersion)
	}
	plaintext, err := s.cipher.Decrypt(row.EncryptedValue)
	if err != nil {
		return domaincredential.Secret{}, fmt.Errorf("decrypt provider credential: %w", err)
	}
	secret, err := domaincredential.NewSecret(string(plaintext))
	if err != nil {
		return domaincredential.Secret{}, fmt.Errorf("invalid decrypted credential: %w", err)
	}
	return secret, nil
}

func (s *SQLiteStore) Delete(ctx context.Context, id provider.ID) error {
	if err := s.queries.DeleteProviderCredential(ctx, string(id)); err != nil {
		return fmt.Errorf("delete provider credential: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Exists(ctx context.Context, id provider.ID) (bool, error) {
	exists, err := s.queries.ProviderCredentialExists(ctx, string(id))
	if err != nil {
		return false, fmt.Errorf("check provider credential: %w", err)
	}
	return exists, nil
}

var _ appcredential.Store = (*SQLiteStore)(nil)
