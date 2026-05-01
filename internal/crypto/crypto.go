package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	// NonceSize is the size of a GCM nonce (12 bytes).
	NonceSize = 12
	// KeySize is the size of an AES-256 key (32 bytes).
	KeySize = 32
)

var (
	ErrInvalidKeySize    = errors.New("crypto: encryption master key must be 32 bytes (base64 encoded)")
	ErrInvalidCiphertext = errors.New("crypto: ciphertext too short to be valid")
	ErrDecryptionFailed  = errors.New("crypto: decryption failed (ciphertext tampered or wrong key)")
)

// EncryptedPayload holds a versioned ciphertext suitable for storage.
type EncryptedPayload struct {
	// Version tracks the key version / nonce derivation. 0 means "no versioning".
	Version uint32 `json:"v"`
	// Nonce is the GCM nonce used for this encryption (base64).
	Nonce string `json:"n"`
	// Ciphertext is the encrypted data (base64).
	Ciphertext string `json:"c"`
}

// NewEncryptedPayload builds an EncryptedPayload from raw ciphertext and a version.
func NewEncryptedPayload(version uint32, nonce, ciphertext []byte) EncryptedPayload {
	return EncryptedPayload{
		Version:    version,
		Nonce:      base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext),
	}
}

// ParseEncryptedPayload deserializes an EncryptedPayload from base64 strings.
func ParseEncryptedPayload(version uint32, nonceB64, ciphertextB64 string) (EncryptedPayload, error) {
	nonce, err := base64.RawURLEncoding.DecodeString(nonceB64)
	if err != nil {
		return EncryptedPayload{}, fmt.Errorf("decoding nonce: %w", err)
	}
	ct, err := base64.RawURLEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return EncryptedPayload{}, fmt.Errorf("decoding ciphertext: %w", err)
	}
	return NewEncryptedPayload(version, nonce, ct), nil
}

// Encrypt encrypts plaintext using AES-256-GCM with a random nonce.
// The masterKey must be 32 bytes. Returns an EncryptedPayload with version 0.
func Encrypt(masterKey, plaintext []byte) (EncryptedPayload, error) {
	return EncryptWithVersion(masterKey, plaintext, 0)
}

// EncryptWithVersion encrypts plaintext using AES-256-GCM with a random nonce.
// version is included in the payload to support key rotation.
func EncryptWithVersion(masterKey, plaintext []byte, version uint32) (EncryptedPayload, error) {
	if len(masterKey) != KeySize {
		return EncryptedPayload{}, ErrInvalidKeySize
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return EncryptedPayload{}, fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return EncryptedPayload{}, fmt.Errorf("creating GCM: %w", err)
	}

	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return EncryptedPayload{}, fmt.Errorf("generating nonce: %w", err)
	}

	// Prepend version to plaintext so it is authenticated by GCM along with the ciphertext.
	// We encode the version as a 4-byte big-endian prefix.
	prefixed := make([]byte, 4+len(plaintext))
	putVersion(prefixed[:4], version)
	copy(prefixed[4:], plaintext)

	ciphertext := gcm.Seal(nil, nonce, prefixed, nil)

	return NewEncryptedPayload(version, nonce, ciphertext), nil
}

// Decrypt decrypts a payload produced by Encrypt/EncryptWithVersion.
// version is ignored at the crypto layer; callers can read it from the payload.
func Decrypt(masterKey []byte, payload EncryptedPayload) ([]byte, error) {
	return DecryptPayload(masterKey, payload.Version, payload.Nonce, payload.Ciphertext)
}

// DecryptPayload decrypts ciphertext given a version and base64-encoded nonce/ciphertext.
func DecryptPayload(masterKey []byte, version uint32, nonceB64, ciphertextB64 string) ([]byte, error) {
	if len(masterKey) != KeySize {
		return nil, ErrInvalidKeySize
	}

	nonce, err := base64.RawURLEncoding.DecodeString(nonceB64)
	if err != nil {
		return nil, fmt.Errorf("decoding nonce: %w", err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("decoding ciphertext: %w", err)
	}
	if len(ciphertext) < 4 {
		return nil, ErrInvalidCiphertext
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	prefixed, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	// The first 4 bytes are the version prefix; they were authenticated by GCM.
	// We could validate that prefixed[:4] matches the encoded version if desired,
	// but the issue only requires key rotation support via nonce versioning,
	// so we just strip the prefix.
	payloadVersion := getVersion(prefixed[:4])
	if payloadVersion != version {
		// This shouldn't happen with a correct implementation, but guard anyway.
		return nil, fmt.Errorf("version mismatch: expected %d, got %d", version, payloadVersion)
	}

	return prefixed[4:], nil
}

// putVersion writes v as big-endian into buf (len(buf) must be >= 4).
func putVersion(buf []byte, v uint32) {
	buf[0] = byte(v >> 24)
	buf[1] = byte(v >> 16)
	buf[2] = byte(v >> 8)
	buf[3] = byte(v)
}

// getVersion reads a big-endian uint32 from buf (len(buf) must be >= 4).
func getVersion(buf []byte) uint32 {
	return uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
}

// MustEncryptMasterKey reads and decodes the ENCRYPTION_MASTER_KEY env var.
// It panics if the variable is missing or the key is not valid.
func MustEncryptMasterKey() []byte {
	raw := os.Getenv("ENCRYPTION_MASTER_KEY")
	if raw == "" {
		panic("ENCRYPTION_MASTER_KEY environment variable is not set")
	}
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		panic(fmt.Sprintf("ENCRYPTION_MASTER_KEY is not valid base64: %v", err))
	}
	if len(key) != KeySize {
		panic(fmt.Sprintf("ENCRYPTION_MASTER_KEY must be %d bytes, got %d", KeySize, len(key)))
	}
	return key
}

// NewEncryptor returns an EncryptedPayload for the given plaintext using the loaded key.
// Convenience wrapper around EncryptWithVersion using version 0.
func NewEncryptor(masterKey []byte, plaintext []byte) (EncryptedPayload, error) {
	return EncryptWithVersion(masterKey, plaintext, 0)
}
