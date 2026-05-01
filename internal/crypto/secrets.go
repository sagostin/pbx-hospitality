package crypto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// ErrSecretNotFound is returned when a secret does not exist in the store.
var ErrSecretNotFound = errors.New("secret not found")

// SecretRecord represents a row in the encrypted_secrets table.
type SecretRecord struct {
	ID        int64
	Namespace string // e.g. "pbx", "pms", "tenant:<id>"
	Name      string
	KeyVersion uint32
	Nonce     string // base64 RawURL
	Ciphertext string // base64 RawURL
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SecretsStore provides encrypted credential storage backed by a database table.
//
// The table schema (created automatically via InitSchema):
//
//	CREATE TABLE IF NOT EXISTS encrypted_secrets (
//	    id         BIGSERIAL PRIMARY KEY,
//	    namespace  TEXT NOT NULL,
//	    name       TEXT NOT NULL,
//	    key_version INTEGER NOT NULL DEFAULT 0,
//	    nonce      TEXT NOT NULL,
//	    ciphertext TEXT NOT NULL,
//	    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
//	    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
//	    UNIQUE (namespace, name)
//	);
//
//	CREATE INDEX IF NOT EXISTS idx_encrypted_secrets_ns_name ON encrypted_secrets (namespace, name);
type SecretsStore struct {
	pool      *pgxpool.Pool
	masterKey []byte
}

// NewSecretsStore creates a SecretsStore backed by the given pgxpool.
// The masterKey must be 32 bytes.
func NewSecretsStore(pool *pgxpool.Pool, masterKey []byte) (*SecretsStore, error) {
	if len(masterKey) != KeySize {
		return nil, ErrInvalidKeySize
	}
	return &SecretsStore{pool: pool, masterKey: masterKey}, nil
}

// InitSchema creates the encrypted_secrets table if it does not exist.
func (s *SecretsStore) InitSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS encrypted_secrets (
		id          BIGSERIAL PRIMARY KEY,
		namespace   TEXT    NOT NULL,
		name        TEXT    NOT NULL,
		key_version INTEGER NOT NULL DEFAULT 0,
		nonce       TEXT    NOT NULL,
		ciphertext  TEXT    NOT NULL,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (namespace, name)
	);
	CREATE INDEX IF NOT EXISTS idx_encrypted_secrets_ns_name ON encrypted_secrets (namespace, name);
	`
	_, err := s.pool.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("creating encrypted_secrets schema: %w", err)
	}
	return nil
}

// StoreSecret encrypts and stores a secret. If the secret already exists it is updated.
func (s *SecretsStore) StoreSecret(ctx context.Context, namespace, name string, plaintext []byte) error {
	return s.StoreSecretWithVersion(ctx, namespace, name, plaintext, 0)
}

// StoreSecretWithVersion encrypts and stores a secret with an explicit key version.
// This should be used during key rotation to record which version of the master key
// was used to encrypt the value.
func (s *SecretsStore) StoreSecretWithVersion(ctx context.Context, namespace, name string, plaintext []byte, version uint32) error {
	payload, err := EncryptWithVersion(s.masterKey, plaintext, version)
	if err != nil {
		return fmt.Errorf("encrypting secret: %w", err)
	}

	_, err = s.pool.ExecContext(ctx, `
		INSERT INTO encrypted_secrets (namespace, name, key_version, nonce, ciphertext, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (namespace, name) DO UPDATE SET
			key_version = EXCLUDED.key_version,
			nonce       = EXCLUDED.nonce,
			ciphertext  = EXCLUDED.ciphertext,
			updated_at  = NOW()
	`, namespace, name, payload.Version, payload.Nonce, payload.Ciphertext)
	if err != nil {
		return fmt.Errorf("upserting secret: %w", err)
	}

	log.Debug().Str("namespace", namespace).Str("name", name).Msg("Secret stored")
	return nil
}

// GetSecret retrieves and decrypts a secret. Returns ErrSecretNotFound if missing.
func (s *SecretsStore) GetSecret(ctx context.Context, namespace, name string) ([]byte, error) {
	var rec SecretRecord
	err := s.pool.QueryRowContext(ctx, `
		SELECT id, namespace, name, key_version, nonce, ciphertext, created_at, updated_at
		FROM encrypted_secrets WHERE namespace = $1 AND name = $2
	`, namespace, name).Scan(
		&rec.ID, &rec.Namespace, &rec.Name, &rec.KeyVersion,
		&rec.Nonce, &rec.Ciphertext, &rec.CreatedAt, &rec.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, ErrSecretNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying secret: %w", err)
	}

	plaintext, err := DecryptPayload(s.masterKey, rec.KeyVersion, rec.Nonce, rec.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypting secret: %w", err)
	}

	return plaintext, nil
}

// RotateSecret re-encrypts an existing secret with a new key version and stores it.
// If the secret does not exist, returns ErrSecretNotFound.
// This does NOT invalidate the old version; callers should ensure the old key
// material remains available for decryption of older ciphertexts during a
// rotation window, or re-encrypt all secrets sequentially during a key change.
func (s *SecretsStore) RotateSecret(ctx context.Context, namespace, name string, newPlaintext []byte, newVersion uint32) error {
	// Verify the secret exists first
	var exists bool
	err := s.pool.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM encrypted_secrets WHERE namespace = $1 AND name = $2)
	`, namespace, name).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking secret existence: %w", err)
	}
	if !exists {
		return ErrSecretNotFound
	}

	return s.StoreSecretWithVersion(ctx, namespace, name, newPlaintext, newVersion)
}

// DeleteSecret removes a secret. Returns ErrSecretNotFound if it did not exist.
func (s *SecretsStore) DeleteSecret(ctx context.Context, namespace, name string) error {
	result, err := s.pool.ExecContext(ctx, `
		DELETE FROM encrypted_secrets WHERE namespace = $1 AND name = $2
	`, namespace, name)
	if err != nil {
		return fmt.Errorf("deleting secret: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return ErrSecretNotFound
	}
	return nil
}

// ListSecrets returns the metadata (namespace, name, version, timestamps) of all
// secrets in a namespace. The plaintext values are NOT included.
func (s *SecretsStore) ListSecrets(ctx context.Context, namespace string) ([]SecretRecord, error) {
	rows, err := s.pool.QueryContext(ctx, `
		SELECT id, namespace, name, key_version, nonce, ciphertext, created_at, updated_at
		FROM encrypted_secrets WHERE namespace = $1 ORDER BY name
	`, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}
	defer rows.Close()

	var records []SecretRecord
	for rows.Next() {
		var r SecretRecord
		if err := rows.Scan(&r.ID, &r.Namespace, &r.Name, &r.KeyVersion,
			&r.Nonce, &r.Ciphertext, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning secret row: %w", err)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// SecretJSON is a helper for storing JSON-serialisable structs as encrypted secrets.
// Use StoreJSON/GetJSON for convenience.
func (s *SecretsStore) StoreJSON(ctx context.Context, namespace, name string, v interface{}) error {
	plaintext, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshalling JSON: %w", err)
	}
	return s.StoreSecret(ctx, namespace, name, plaintext)
}

// GetJSON retrieves a secret and deserialises it as JSON into v.
func (s *SecretsStore) GetJSON(ctx context.Context, namespace, name string, v interface{}) error {
	plaintext, err := s.GetSecret(ctx, namespace, name)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(plaintext, v); err != nil {
		return fmt.Errorf("unmarshalling JSON: %w", err)
	}
	return nil
}
