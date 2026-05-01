package crypto

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrSecretNotFound is returned when a secret does not exist in the store.
var ErrSecretNotFound = errors.New("crypto: secret not found")

// SecretsDB provides encrypted secret storage backed by PostgreSQL.
// It wraps a pgxpool to allow the crypto package to perform DB operations
// without importing the full db package.
type SecretsDB struct {
	pool *pgxpool.Pool
}

// NewSecretsDB creates a SecretsDB wrapping the given connection pool.
func NewSecretsDB(pool *pgxpool.Pool) *SecretsDB {
	return &SecretsDB{pool: pool}
}

// encryptedSecretRow represents a row in the encrypted_secrets table.
type encryptedSecretRow struct {
	SiteID       string
	KeyName      string
	Ciphertext   []byte
	Nonce        []byte
	RotatedAt    time.Time
	PreviousNonce []byte // retained for decryption during key rotation
}

// StoreSecret encrypts a plaintext secret and upserts it into encrypted_secrets.
// The siteID + keyName uniquely identifies the secret.
func (s *SecretsDB) StoreSecret(ctx context.Context, siteID, keyName, plaintext string) error {
	ciphertext, nonce, err := Encrypt([]byte(plaintext))
	if err != nil {
		return fmt.Errorf("encrypting secret: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO encrypted_secrets (site_id, key_name, ciphertext, nonce, rotated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (site_id, key_name) DO UPDATE SET
			ciphertext = EXCLUDED.ciphertext,
			nonce     = EXCLUDED.nonce,
			rotated_at = EXCLUDED.rotated_at
	`, siteID, keyName, ciphertext, nonce)
	if err != nil {
		return fmt.Errorf("upserting secret: %w", err)
	}
	return nil
}

// GetSecret retrieves and decrypts a secret. Returns ErrSecretNotFound if not found.
func (s *SecretsDB) GetSecret(ctx context.Context, siteID, keyName string) (string, error) {
	var row encryptedSecretRow
	err := s.pool.QueryRow(ctx, `
		SELECT site_id, key_name, ciphertext, nonce, rotated_at
		FROM encrypted_secrets
		WHERE site_id = $1 AND key_name = $2
	`, siteID, keyName).Scan(&row.SiteID, &row.KeyName, &row.Ciphertext, &row.Nonce, &row.RotatedAt)

	if err == pgx.ErrNoRows {
		return "", ErrSecretNotFound
	}
	if err != nil {
		return "", fmt.Errorf("querying secret: %w", err)
	}

	plaintext, err := Decrypt(row.Ciphertext, row.Nonce)
	if err != nil {
		return "", fmt.Errorf("decrypting secret: %w", err)
	}
	return string(plaintext), nil
}

// RotateSecret re-encrypts a secret with a new nonce and updates rotated_at.
// The old nonce is NOT overwritten — it is preserved so that data encrypted with
// the old key can still be decrypted during a key rotation transition period.
// The caller is responsible for triggering any downstream re-encryption of
// data that was encrypted under the old key.
func (s *SecretsDB) RotateSecret(ctx context.Context, siteID, keyName, plaintext string) error {
	// First verify the secret exists
	var oldNonce []byte
	err := s.pool.QueryRow(ctx, `
		SELECT nonce FROM encrypted_secrets WHERE site_id = $1 AND key_name = $2
	`, siteID, keyName).Scan(&oldNonce)
	if err == pgx.ErrNoRows {
		return ErrSecretNotFound
	}
	if err != nil {
		return fmt.Errorf("querying existing secret for rotation: %w", err)
	}

	// Encrypt with new nonce
	ciphertext, nonce, err := Encrypt([]byte(plaintext))
	if err != nil {
		return fmt.Errorf("encrypting rotated secret: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		UPDATE encrypted_secrets
		SET ciphertext = $3,
		    nonce = $4,
		    rotated_at = NOW(),
		    previous_nonce = $5
		WHERE site_id = $1 AND key_name = $2
	`, siteID, keyName, ciphertext, nonce, oldNonce)
	if err != nil {
		return fmt.Errorf("rotating secret: %w", err)
	}
	return nil
}

// DeleteSecret removes a secret from the store.
func (s *SecretsDB) DeleteSecret(ctx context.Context, siteID, keyName string) error {
	result, err := s.pool.Exec(ctx, `
		DELETE FROM encrypted_secrets WHERE site_id = $1 AND key_name = $2
	`, siteID, keyName)
	if err != nil {
		return fmt.Errorf("deleting secret: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrSecretNotFound
	}
	return nil
}
