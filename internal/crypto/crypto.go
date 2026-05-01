package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

var (
	ErrInvalidKeySize   = errors.New("crypto: encryption master key must be 32 bytes (base64-encoded)")
	ErrDecryptionFailed = errors.New("crypto: decryption failed")
)

var masterKey []byte

// Init loads the master key from the ENCRYPTION_MASTER_KEY environment variable.
// It must be called before using any Encrypt/Decrypt functions.
func Init() error {
	keyStr := os.Getenv("ENCRYPTION_MASTER_KEY")
	if keyStr == "" {
		return errors.New("crypto: ENCRYPTION_MASTER_KEY env var is required")
	}
	key, err := base64.StdEncoding.DecodeString(keyStr)
	if err != nil {
		return fmt.Errorf("crypto: failed to base64-decode master key: %w", err)
	}
	if len(key) != 32 {
		return ErrInvalidKeySize
	}
	masterKey = key
	return nil
}

// InitWithKey allows setting the master key directly (useful for testing).
func InitWithKey(key []byte) error {
	if len(key) != 32 {
		return ErrInvalidKeySize
	}
	masterKey = make([]byte, 32)
	copy(masterKey, key)
	return nil
}

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns ciphertext and a 12-byte nonce.
// The nonce is prepended to the ciphertext by the caller if stored together.
func Encrypt(plaintext []byte) (ciphertext, nonce []byte, err error) {
	if masterKey == nil {
		return nil, nil, errors.New("crypto: master key not initialized; call Init() first")
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, nil, fmt.Errorf("crypto: failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("crypto: failed to create GCM: %w", err)
	}

	// GCM standard nonce is 12 bytes
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("crypto: failed to generate nonce: %w", err)
	}

	// Seal appends the ciphertext to dst; we pass a nil dst so it returns just the sealed output
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Decrypt decrypts ciphertext using AES-256-GCM with the given nonce.
func Decrypt(ciphertext, nonce []byte) (plaintext []byte, err error) {
	if masterKey == nil {
		return nil, errors.New("crypto: master key not initialized; call Init() first")
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to create GCM: %w", err)
	}

	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("crypto: nonce must be %d bytes", gcm.NonceSize())
	}

	plaintext, err = gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}
