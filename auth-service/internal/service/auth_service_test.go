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
	creds       map[string]*domain.Credential
	memberships map[string][]string
}

func (m *mockCredentialRepo) UpsertCredential(_ context.Context, input repository.UpsertCredentialInput) error {
	m.creds[input.Email] = &domain.Credential{
		UserID:       input.UserID,
		Email:        input.Email,
		PasswordHash: input.PasswordHash,
	}
	if input.TenantID != "" {
		_ = m.AddMembership(context.TODO(), input.UserID, input.TenantID)
	}
	return nil
}

func (m *mockCredentialRepo) AddMembership(_ context.Context, userID, tenantID string) error {
	if m.memberships == nil {
		m.memberships = make(map[string][]string)
	}
	for _, t := range m.memberships[userID] {
		if t == tenantID {
			return nil
		}
	}
	m.memberships[userID] = append(m.memberships[userID], tenantID)
	return nil
}

func (m *mockCredentialRepo) ListUserMemberships(_ context.Context, userID string) ([]string, error) {
	if m.memberships == nil {
		return nil, nil
	}
	return m.memberships[userID], nil
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
		TenantID:  input.TenantID,
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

type mockSetupTokenRepo struct {
	tokens map[string]*domain.PasswordSetupToken
}

func (m *mockSetupTokenRepo) CreateSetupToken(_ context.Context, input repository.CreateSetupTokenInput) error {
	m.tokens[input.TokenHash] = &domain.PasswordSetupToken{
		ID:        "st_id",
		UserID:    input.UserID,
		TenantID:  input.TenantID,
		Email:     input.Email,
		TokenHash: input.TokenHash,
		ExpiresAt: input.ExpiresAt,
	}
	return nil
}

func (m *mockSetupTokenRepo) FindByTokenHash(_ context.Context, tokenHash string) (*domain.PasswordSetupToken, error) {
	if st, ok := m.tokens[tokenHash]; ok {
		return st, nil
	}
	return nil, domain.ErrTokenNotFound
}

func (m *mockSetupTokenRepo) MarkTokenUsed(_ context.Context, tokenHash string) error {
	if st, ok := m.tokens[tokenHash]; ok {
		if st.UsedAt != nil {
			return domain.ErrTokenAlreadyUsed
		}
		now := time.Now()
		st.UsedAt = &now
		return nil
	}
	return domain.ErrTokenNotFound
}

func testRSAPrivateKey(t *testing.T) string {
	t.Helper()
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	der := x509.MarshalPKCS1PrivateKey(privateKey)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

func setupAuthService(t *testing.T) (*service.AuthService, *service.InternalAuthService, *mockCredentialRepo, *mockTokenRepo, *mockSetupTokenRepo) {
	t.Helper()
	pemStr := testRSAPrivateKey(t)

	jwtMgr, err := crypto.NewJWTManager(pemStr)
	if err != nil {
		t.Fatalf("failed to init JWTManager: %v", err)
	}

	credRepo := &mockCredentialRepo{creds: make(map[string]*domain.Credential), memberships: make(map[string][]string)}
	tokenRepo := &mockTokenRepo{tokens: make(map[string]*domain.RefreshToken)}
	setupRepo := &mockSetupTokenRepo{tokens: make(map[string]*domain.PasswordSetupToken)}
	svc := service.NewAuthService(credRepo, tokenRepo, setupRepo, jwtMgr, nil)
	internalSvc := service.NewInternalAuthService(setupRepo, credRepo)
	return svc, internalSvc, credRepo, tokenRepo, setupRepo
}

func TestAuthService_SetupPasswordAndLogin(t *testing.T) {
	svc, internalSvc, _, _, _ := setupAuthService(t)
	ctx := context.Background()

	rawToken, err := internalSvc.CreatePasswordSetupToken(ctx, service.InternalCreateSetupTokenInput{
		UserID:   "usr_100",
		TenantID: "tnt_200",
		Email:    "user@example.com",
	})
	if err != nil {
		t.Fatalf("CreatePasswordSetupToken failed: %v", err)
	}

	pair, err := svc.SetupPassword(ctx, service.SetupPasswordInput{
		Token:    rawToken,
		Password: "secretpassword",
	})
	if err != nil {
		t.Fatalf("SetupPassword failed: %v", err)
	}

	// Test Login always resolves through workspace selection (exchange token)
	loginRes, err := svc.Login(ctx, service.LoginInput{
		Email:    "user@example.com",
		Password: "secretpassword",
	})
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if loginRes.Status != domain.LoginStatusSelectWorkspace {
		t.Errorf("expected status SELECT_WORKSPACE, got %q", loginRes.Status)
	}
	if loginRes.ExchangeToken == "" {
		t.Errorf("expected a non-empty exchange token")
	}
	if len(loginRes.Workspaces) != 1 || loginRes.Workspaces[0].TenantID != "tnt_200" {
		t.Errorf("expected single workspace tnt_200, got %+v", loginRes.Workspaces)
	}
	if loginRes.TokenPair != nil {
		t.Errorf("expected no direct token pair until workspace is selected")
	}

	// Test Workspace Selection issues a token pair for a member workspace
	pairFromExchange, err := svc.SelectWorkspace(ctx, service.SelectWorkspaceInput{
		ExchangeToken: loginRes.ExchangeToken,
		TenantID:      "tnt_200",
	})
	if err != nil {
		t.Fatalf("SelectWorkspace failed: %v", err)
	}
	if pairFromExchange.AccessToken == "" || pairFromExchange.RefreshToken == "" {
		t.Errorf("expected tokens in workspace-selection response pair")
	}

	// Test Workspace Selection rejects a non-member tenant
	_, err = svc.SelectWorkspace(ctx, service.SelectWorkspaceInput{
		ExchangeToken: loginRes.ExchangeToken,
		TenantID:      "tnt_evil",
	})
	if err == nil {
		t.Errorf("expected error when selecting a tenant the user does not belong to")
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

func TestAuthService_CreatePasswordSetupTokenAndSetupPassword(t *testing.T) {
	svc, internalSvc, _, _, _ := setupAuthService(t)
	ctx := context.Background()

	rawToken, err := internalSvc.CreatePasswordSetupToken(ctx, service.InternalCreateSetupTokenInput{
		UserID:   "usr_setup_100",
		TenantID: "tnt_setup_200",
		Email:    "setup@example.com",
	})
	if err != nil {
		t.Fatalf("CreatePasswordSetupToken failed: %v", err)
	}
	if rawToken == "" {
		t.Fatalf("expected non-empty raw setup token")
	}

	// Submit setup password using token
	pair, err := svc.SetupPassword(ctx, service.SetupPasswordInput{
		Token:    rawToken,
		Password: "newpassword123",
	})
	if err != nil {
		t.Fatalf("SetupPassword failed: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Errorf("expected tokens in setup password response pair")
	}

	// Attempting to reuse the token should fail
	_, err = svc.SetupPassword(ctx, service.SetupPasswordInput{
		Token:    rawToken,
		Password: "newpassword123",
	})
	if err == nil {
		t.Errorf("expected error when reusing setup token")
	}

	// Login with newly setup password should resolve through workspace selection
	loginRes, err := svc.Login(ctx, service.LoginInput{
		Email:    "setup@example.com",
		Password: "newpassword123",
	})
	if err != nil {
		t.Fatalf("Login with setup password failed: %v", err)
	}
	if loginRes.Status != domain.LoginStatusSelectWorkspace {
		t.Errorf("expected status SELECT_WORKSPACE, got %q", loginRes.Status)
	}
	if loginRes.ExchangeToken == "" {
		t.Errorf("expected a non-empty exchange token")
	}
	if len(loginRes.Workspaces) != 1 || loginRes.Workspaces[0].TenantID != "tnt_setup_200" {
		t.Errorf("expected single workspace tnt_setup_200, got %+v", loginRes.Workspaces)
	}

	exchangePair, err := svc.SelectWorkspace(ctx, service.SelectWorkspaceInput{
		ExchangeToken: loginRes.ExchangeToken,
		TenantID:      "tnt_setup_200",
	})
	if err != nil {
		t.Fatalf("SelectWorkspace failed: %v", err)
	}
	if exchangePair.AccessToken == "" {
		t.Errorf("expected access token from workspace selection")
	}
}

