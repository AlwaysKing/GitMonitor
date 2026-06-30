package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const keyFileName = "master.key"

func LoadOrCreateKey(configDir string) ([]byte, error) {
	if envKey := os.Getenv("GTI_MASTER_KEY"); envKey != "" {
		decoded, err := base64.StdEncoding.DecodeString(envKey)
		if err == nil && len(decoded) == 32 {
			return decoded, nil
		}
		return nil, errors.New("GTI_MASTER_KEY must be base64 encoded 32 bytes")
	}

	keyPath := filepath.Join(configDir, keyFileName)
	if data, err := os.ReadFile(keyPath); err == nil {
		decoded, err := base64.StdEncoding.DecodeString(string(data))
		if err != nil {
			return nil, fmt.Errorf("decode master key: %w", err)
		}
		if len(decoded) != 32 {
			return nil, errors.New("master key length must be 32 bytes")
		}
		return decoded, nil
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}

	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return key, nil
}

func Encrypt(key []byte, plaintext string) (string, error) {
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
	encrypted := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func Decrypt(key []byte, ciphertext string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
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
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, payload := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, payload, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
