package crypto

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptAESGCM_Success(t *testing.T) {
	masterKey := []byte("my_super_secret_master_key_1234")
	plaintext := []byte(`{"stripe":{"api_key":"sk_live_123456789"}}`)

	ciphertext, err := EncryptAESGCM(plaintext, masterKey)
	if err != nil {
		t.Fatalf("EncryptAESGCM failed: %v", err)
	}

	if bytes.Equal(ciphertext, plaintext) {
		t.Fatalf("ciphertext should not match plaintext")
	}

	decrypted, err := DecryptAESGCM(ciphertext, masterKey)
	if err != nil {
		t.Fatalf("DecryptAESGCM failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("expected decrypted '%s', got '%s'", string(plaintext), string(decrypted))
	}
}

func TestDecryptAESGCM_WrongKey_Fails(t *testing.T) {
	key1 := []byte("key_one_12345678901234567890123456")
	key2 := []byte("key_two_12345678901234567890123456")
	plaintext := []byte("secret data")

	ciphertext, err := EncryptAESGCM(plaintext, key1)
	if err != nil {
		t.Fatalf("EncryptAESGCM failed: %v", err)
	}

	_, err = DecryptAESGCM(ciphertext, key2)
	if err == nil {
		t.Fatalf("expected decryption to fail with wrong key")
	}
}
