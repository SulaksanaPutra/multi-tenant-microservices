package service_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"auth-service/internal/crypto"
	"auth-service/internal/domain"
	"auth-service/internal/repository"
	"auth-service/internal/service"
)

type mockCredentialRepo struct {
	creds map[string]*domain.Credential
}

func (m *mockCredentialRepo) UpsertCredential(_ context.Context, input repository.UpsertCredentialInput) error {
	m.creds[input.Email] = &domain.Credential{
		UserID:       input.UserID,
		TenantID:     input.TenantID,
		Email:        input.Email,
		PasswordHash: input.PasswordHash,
	}
	return nil
}

func (m *mockCredentialRepo) FindByEmail(_ context.Context, email string) (*domain.Credential, error) {
	if cred, ok := m.creds[email]; ok {
		return cred, nil
	}
	return nil, domain.ErrCredentialNotFound
}

func (m *mockCredentialRepo) FindByUserID(_ context.Context, userID string) (*domain.Credential, error) {
	for _, cred := range m.creds {
		if cred.UserID == userID {
			return cred, nil
		}
	}
	return nil, domain.ErrCredentialNotFound
}

type mockTokenRepo struct {
	tokens map[string]*domain.RefreshToken
}

func (m *mockTokenRepo) CreateRefreshToken(_ context.Context, input repository.CreateRefreshTokenInput) error {
	m.tokens[input.TokenHash] = &domain.RefreshToken{
		ID:        "rt_id",
		UserID:    input.UserID,
		TokenHash: input.TokenHash,
		ExpiresAt: input.ExpiresAt,
	}
	return nil
}

func (m *mockTokenRepo) FindByTokenHash(_ context.Context, tokenHash string) (*domain.RefreshToken, error) {
	if rt, ok := m.tokens[tokenHash]; ok {
		return rt, nil
	}
	return nil, domain.ErrTokenNotFound
}

func (m *mockTokenRepo) RevokeRefreshToken(_ context.Context, input repository.RevokeRefreshTokenInput) error {
	if rt, ok := m.tokens[input.TokenHash]; ok {
		now := time.Now()
		rt.RevokedAt = &now
		return nil
	}
	return domain.ErrTokenNotFound
}

func (m *mockTokenRepo) DeleteRefreshToken(_ context.Context, input repository.DeleteRefreshTokenInput) error {
	delete(m.tokens, input.TokenHash)
	return nil
}

func setupAuthService(t *testing.T) (*service.AuthService, *mockCredentialRepo, *mockTokenRepo) {
	t.Helper()
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	der := x509.MarshalPKCS1PrivateKey(privateKey)
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))

	jwtMgr, err := crypto.NewJWTManager(pemStr)
	if err != nil {
		t.Fatalf("failed to init JWTManager: %v", err)
	}

	credRepo := &mockCredentialRepo{creds: make(map[string]*domain.Credential)}
	tokenRepo := &mockTokenRepo{tokens: make(map[string]*domain.RefreshToken)}
	svc := service.NewAuthService(credRepo, tokenRepo, jwtMgr)
	return svc, credRepo, tokenRepo
}

func TestAuthService_SetCredentialsAndLogin(t *testing.T) {
	svc, _, _ := setupAuthService(t)
	ctx := context.Background()

	err := svc.SetCredentials(ctx, service.SetCredentialsInput{
		UserID:   "usr_100",
		TenantID: "tnt_200",
		Email:    "user@example.com",
		Password: "secretpassword",
	})
	if err != nil {
		t.Fatalf("SetCredentials failed: %v", err)
	}

	// Test Login Success
	pair, err := svc.Login(ctx, service.LoginInput{
		Email:    "user@example.com",
		Password: "secretpassword",
	})
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Errorf("expected tokens in response pair")
	}

	// Test Login Failure (Wrong password)
	_, err = svc.Login(ctx, service.LoginInput{
		Email:    "user@example.com",
		Password: "wrongpassword",
	})
	if err == nil {
		t.Errorf("expected error for wrong password")
	}

	// Test Refresh Success
	refreshed, err := svc.RefreshToken(ctx, service.RefreshTokenInput{
		RefreshToken: pair.RefreshToken,
	})
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}
	if refreshed.AccessToken == "" {
		t.Errorf("expected new access token after refresh")
	}

	// Test Logout
	err = svc.Logout(ctx, service.LogoutInput{
		RefreshToken: refreshed.RefreshToken,
	})
	if err != nil {
		t.Fatalf("Logout failed: %v", err)
	}
}
