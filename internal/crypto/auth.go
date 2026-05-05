package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/argon2"
)

var errInvalidFormat = errors.New("crypto: invalid argon2 hash format")

const (
	authCodeSaltLen = 16
	authCodeKeyLen  = 32
	authCodeTime    = 1
	authCodeMem     = 64 * 1024
	authCodeThreads = 4
)

func HashAuthCode(code string) (encoded string, err error) {
	salt := make([]byte, authCodeSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	hash := argon2.IDKey([]byte(code), salt, authCodeTime, authCodeMem, authCodeThreads, authCodeKeyLen)

	saltB64 := base64.StdEncoding.EncodeToString(salt)
	hashB64 := base64.StdEncoding.EncodeToString(hash)

	return "$argon2id$v=13$m=64,t=1,p=4$" + saltB64 + "$" + hashB64, nil
}

func VerifyAuthCode(code, encoded string) bool {
	expected, err := decodeArgon2Hash(encoded)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(code), expected) == 1
}

func decodeArgon2Hash(encoded string) ([]byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return nil, errInvalidFormat
	}

	salt, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return nil, err
	}
	hash, err := base64.StdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, err
	}

	return argon2.IDKey(salt, hash, authCodeTime, authCodeMem, authCodeThreads, authCodeKeyLen), nil
}