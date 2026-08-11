package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"payment-service/internal/domain"
)

func deriveKey(masterKey []byte) []byte {
	if len(masterKey) == 32 {
		return masterKey
	}
	hash := sha256.Sum256(masterKey)
	return hash[:]
}

func EncryptAESGCM(plaintext []byte, masterKey []byte) ([]byte, error) {
	if len(masterKey) == 0 {
		return nil, domain.ErrEmptyMasterKey
	}

	key := deriveKey(masterKey)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate random nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

func DecryptAESGCM(ciphertext []byte, masterKey []byte) ([]byte, error) {
	if len(masterKey) == 0 {
		return nil, domain.ErrEmptyMasterKey
	}

	key := deriveKey(masterKey)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, domain.ErrInvalidCiphertext
	}

	nonce, encryptedPayload := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, encryptedPayload, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt payload: %w", domain.ErrInvalidCiphertext)
	}

	return plaintext, nil
}
