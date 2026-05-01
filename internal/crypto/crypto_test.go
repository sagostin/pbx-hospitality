package crypto

import (
	"bytes"
	"testing"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	// Set up a fixed 32-byte key for testing
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	if err := InitWithKey(key); err != nil {
		t.Fatalf("InitWithKey failed: %v", err)
	}

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{
			name:      "basic hello world",
			plaintext: []byte("hello world"),
		},
		{
			name:      "empty plaintext",
			plaintext: []byte{},
		},
		{
			name:      "large plaintext",
			plaintext: bytes.Repeat([]byte("x"), 1<<20), // 1 MB
		},
		{
			name:      "special characters",
			plaintext: []byte("!@#$%^&*()_+-=[]{}|;':\",./<>?`~\n\t\r"),
		},
		{
			name:      "unicode",
			plaintext: []byte("こんにちは世界 🔐 ¥€£¢ 中文русский"),
		},
		{
			name:      "binary",
			plaintext: []byte{0x00, 0xff, 0xfe, 0x01, 0x80, 0x7f, 0xc0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ciphertext, nonce, err := Encrypt(tc.plaintext)
			if err != nil {
				t.Fatalf("Encrypt failed: %v", err)
			}

			if len(nonce) != 12 {
				t.Errorf("expected nonce length 12, got %d", len(nonce))
			}

			decrypted, err := Decrypt(ciphertext, nonce)
			if err != nil {
				t.Fatalf("Decrypt failed: %v", err)
			}

			if !bytes.Equal(decrypted, tc.plaintext) {
				t.Errorf("decrypted != plaintext\ngot:      %v\nnwant:   %v", decrypted, tc.plaintext)
			}
		})
	}
}

func TestDecrypt_InvalidNonce(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	InitWithKey(key)

	plaintext := []byte("secret data")
	ciphertext, _, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Use a wrong nonce
	wrongNonce := []byte("wrongnonce12")
	_, err = Decrypt(ciphertext, wrongNonce)
	if err == nil {
		t.Error("expected Decrypt to fail with wrong nonce, got nil")
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	InitWithKey(key)

	plaintext := []byte("sensitive info")
	ciphertext, nonce, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Tamper with the ciphertext
	ciphertext[0] ^= 0xff

	_, err = Decrypt(ciphertext, nonce)
	if err == nil {
		t.Error("expected Decrypt to fail with tampered ciphertext, got nil")
	}
}

func TestInitWithKey_InvalidSize(t *testing.T) {
	tests := []struct {
		name string
		key  []byte
	}{
		{"too short", []byte("only16bytes")},
		{"too long", bytes.Repeat([]byte("a"), 64)},
		{"empty", []byte{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := InitWithKey(tc.key)
			if err == nil {
				t.Error("expected InitWithKey to fail for invalid key size, got nil")
			}
		})
	}
}

func TestEncrypt_NotInitialized(t *testing.T) {
	// Reset masterKey to nil
	masterKey = nil

	_, _, err := Encrypt([]byte("test"))
	if err == nil {
		t.Error("expected Encrypt to fail when not initialized, got nil")
	}
}

func TestDecrypt_NotInitialized(t *testing.T) {
	// Reset masterKey to nil
	masterKey = nil

	_, err := Decrypt([]byte("ciphertext"), []byte("12345678"))
	if err == nil {
		t.Error("expected Decrypt to fail when not initialized, got nil")
	}
}

func TestEncrypt_NonceUniqueness(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	InitWithKey(key)

	plaintext := []byte("same plaintext")
	var nonces [][]byte

	for i := 0; i < 100; i++ {
		_, nonce, err := Encrypt(plaintext)
		if err != nil {
			t.Fatalf("Encrypt failed: %v", err)
		}

		for _, existing := range nonces {
			if bytes.Equal(existing, nonce) {
				t.Errorf("nonce collision detected after %d iterations", i+1)
			}
		}
		nonces = append(nonces, nonce)
	}
}
