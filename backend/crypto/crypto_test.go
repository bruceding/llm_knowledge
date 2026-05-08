package crypto

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath = filepath.Join(tmpDir, "encrypt.key")

	plaintext := "my-secret-password"
	ciphertext, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if ciphertext == plaintext {
		t.Fatal("ciphertext should not equal plaintext")
	}

	decrypted, err := Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestEncryptDecryptWithEnvKey(t *testing.T) {
	os.Setenv("ENCRYPT_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	defer os.Unsetenv("ENCRYPT_KEY")
	cachedKey = nil

	plaintext := "another-secret"
	ciphertext, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	decrypted, err := Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("expected %q, got %q", plaintext, decrypted)
	}

	cachedKey = nil
}

func TestDecryptWithWrongKey(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath = filepath.Join(tmpDir, "encrypt.key")
	cachedKey = nil

	plaintext := "secret"
	ciphertext, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	os.Remove(keyPath)
	cachedKey = nil

	_, err = Decrypt(ciphertext)
	if err == nil {
		t.Fatal("Decrypt should fail with wrong key")
	}
}

func TestEncryptEmptyString(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath = filepath.Join(tmpDir, "encrypt.key")
	cachedKey = nil

	ciphertext, err := Encrypt("")
	if err != nil {
		t.Fatalf("Encrypt empty string failed: %v", err)
	}

	decrypted, err := Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != "" {
		t.Fatalf("expected empty string, got %q", decrypted)
	}
}
