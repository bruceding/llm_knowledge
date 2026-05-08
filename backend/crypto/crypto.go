package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	cachedKey []byte
	keyMu     sync.Mutex
	keyPath   string
)

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	keyPath = filepath.Join(home, ".llm-knowledge", "encrypt.key")
}

func getKey() ([]byte, error) {
	keyMu.Lock()
	defer keyMu.Unlock()

	if cachedKey != nil {
		return cachedKey, nil
	}

	if envKey := os.Getenv("ENCRYPT_KEY"); envKey != "" {
		key, err := hex.DecodeString(envKey)
		if err != nil {
			return nil, fmt.Errorf("ENCRYPT_KEY must be hex-encoded: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("ENCRYPT_KEY must be 32 bytes (64 hex chars), got %d", len(key))
		}
		cachedKey = key
		return cachedKey, nil
	}

	if data, err := os.ReadFile(keyPath); err == nil {
		key, err := hex.DecodeString(string(data))
		if err == nil && len(key) == 32 {
			cachedKey = key
			return cachedKey, nil
		}
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create key directory: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0600); err != nil {
		return nil, fmt.Errorf("failed to write key file: %w", err)
	}

	fmt.Printf("[crypto] Generated new encryption key at %s\n", keyPath)
	cachedKey = key
	return cachedKey, nil
}

func Encrypt(plaintext string) (string, error) {
	key, err := getKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func Decrypt(ciphertext string) (string, error) {
	key, err := getKey()
	if err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
