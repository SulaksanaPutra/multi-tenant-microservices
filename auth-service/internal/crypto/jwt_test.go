package crypto_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"auth-service/internal/crypto"
)

func generateTestPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(privateKey)
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: der,
	}))
}

func TestJWTManager_SignAndVerify(t *testing.T) {
	pemStr := generateTestPrivateKeyPEM(t)
	jwtMgr, err := crypto.NewJWTManager(pemStr)
	if err != nil {
		t.Fatalf("failed to create JWTManager: %v", err)
	}

	userID := "usr_123"
	tenantID := "tnt_456"
	email := "test@example.com"
	jti := "jti_789"

	tokenStr, err := jwtMgr.SignAccessToken(userID, tenantID, email, jti)
	if err != nil {
		t.Fatalf("failed to sign access token: %v", err)
	}

	claims, err := jwtMgr.VerifyAccessToken(tokenStr)
	if err != nil {
		t.Fatalf("failed to verify access token: %v", err)
	}

	if claims.UserID != userID {
		t.Errorf("expected UserID '%s', got '%s'", userID, claims.UserID)
	}
	if claims.TenantID != tenantID {
		t.Errorf("expected TenantID '%s', got '%s'", tenantID, claims.TenantID)
	}
	if claims.Email != email {
		t.Errorf("expected Email '%s', got '%s'", email, claims.Email)
	}
	if claims.JTI != jti {
		t.Errorf("expected JTI '%s', got '%s'", jti, claims.JTI)
	}
}

func TestJWTManager_BuildJWKS(t *testing.T) {
	pemStr := generateTestPrivateKeyPEM(t)
	jwtMgr, err := crypto.NewJWTManager(pemStr)
	if err != nil {
		t.Fatalf("failed to create JWTManager: %v", err)
	}

	jwksBytes, err := jwtMgr.BuildJWKS()
	if err != nil {
		t.Fatalf("failed to build JWKS: %v", err)
	}

	if len(jwksBytes) == 0 {
		t.Errorf("expected non-empty JWKS bytes")
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	raw1, hash1, err := crypto.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("failed to generate refresh token: %v", err)
	}
	if raw1 == "" || hash1 == "" {
		t.Errorf("expected non-empty raw and hash tokens")
	}

	raw2, hash2, _ := crypto.GenerateRefreshToken()
	if raw1 == raw2 {
		t.Errorf("expected unique raw tokens, got collision")
	}
	if hash1 == hash2 {
		t.Errorf("expected unique hashes, got collision")
	}

	// Verify hashing function consistency
	computedHash := crypto.HashRefreshToken(raw1)
	if computedHash != hash1 {
		t.Errorf("expected HashRefreshToken(%s) = %s, got %s", raw1, hash1, computedHash)
	}
}
