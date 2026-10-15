package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

const (
	// EncryptionPrefix marks ciphertext encrypted with our AES-256-GCM v1 scheme.
	// Allows seamless backward compatibility for legacy plaintext records in the database.
	EncryptionPrefix = "enc:v1:"
	defaultFallback  = "messagerail-default-aes256-internal-secret-key-2026"
)

func deriveKey(key string) []byte {
	if strings.TrimSpace(key) == "" {
		key = defaultFallback
	}
	hash := sha256.Sum256([]byte(key))
	return hash[:]
}

// EncryptBytes encrypts plaintext bytes using AES-256-GCM and returns prefixed base64 bytes.
func EncryptBytes(plainBytes []byte, secretKey string) ([]byte, error) {
	if len(plainBytes) == 0 {
		return plainBytes, nil
	}

	key := deriveKey(secretKey)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	// Seal appends ciphertext to nonce
	sealed := gcm.Seal(nonce, nonce, plainBytes, nil)
	encoded := base64.StdEncoding.EncodeToString(sealed)
	// Return as a JSON-encoded string literal so PostgreSQL JSONB accepts it natively
	return []byte(`"` + EncryptionPrefix + encoded + `"`), nil
}

// DecryptBytes decrypts AES-256-GCM prefixed data. If the data is not prefixed,
// it is treated as legacy unencrypted data and returned as-is.
func DecryptBytes(cipherBytes []byte, secretKey string) ([]byte, error) {
	if len(cipherBytes) == 0 {
		return cipherBytes, nil
	}

	dataStr := strings.TrimSpace(string(cipherBytes))
	// Strip optional surrounding quotes if scanned from PostgreSQL JSONB
	dataStr = strings.Trim(dataStr, "\"")

	if !strings.HasPrefix(dataStr, EncryptionPrefix) {
		// Legacy unencrypted plaintext — return untouched
		return cipherBytes, nil
	}

	encoded := strings.TrimPrefix(dataStr, EncryptionPrefix)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("failed to base64 decode encrypted payload")
	}

	key := deriveKey(secretKey)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return nil, errors.New("ciphertext payload too short")
	}

	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("failed to decrypt ciphertext: invalid key or corrupted payload")
	}

	return plaintext, nil
}
