package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"auth-service/internal/crypto"
	"auth-service/internal/domain"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockAuthService struct {
	SetupPasswordFn   func(ctx context.Context, input service.SetupPasswordInput) (*service.TokenPair, error)
	LoginFn           func(ctx context.Context, input service.LoginInput) (*service.LoginOutput, error)
	SelectWorkspaceFn func(ctx context.Context, input service.SelectWorkspaceInput) (*service.TokenPair, error)
	RefreshTokenFn    func(ctx context.Context, input service.RefreshTokenInput) (*service.TokenPair, error)
	LogoutFn          func(ctx context.Context, input service.LogoutInput) error
}

func (m *mockAuthService) SetupPassword(ctx context.Context, input service.SetupPasswordInput) (*service.TokenPair, error) {
	if m.SetupPasswordFn != nil {
		return m.SetupPasswordFn(ctx, input)
	}
	return nil, nil
}

func (m *mockAuthService) Login(ctx context.Context, input service.LoginInput) (*service.LoginOutput, error) {
	if m.LoginFn != nil {
		return m.LoginFn(ctx, input)
	}
	return nil, nil
}

func (m *mockAuthService) SelectWorkspace(ctx context.Context, input service.SelectWorkspaceInput) (*service.TokenPair, error) {
	if m.SelectWorkspaceFn != nil {
		return m.SelectWorkspaceFn(ctx, input)
	}
	return nil, nil
}

func (m *mockAuthService) RefreshToken(ctx context.Context, input service.RefreshTokenInput) (*service.TokenPair, error) {
	if m.RefreshTokenFn != nil {
		return m.RefreshTokenFn(ctx, input)
	}
	return nil, nil
}

func (m *mockAuthService) Logout(ctx context.Context, input service.LogoutInput) error {
	if m.LogoutFn != nil {
		return m.LogoutFn(ctx, input)
	}
	return nil
}

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

func init() {
	gin.SetMode(gin.TestMode)
}

func TestAuthHandler_SetupPassword(t *testing.T) {
	jwtMgr, _ := crypto.NewJWTManager(generateTestPrivateKeyPEM(t))

	t.Run("validation failure (short password)", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		h := NewAuthHandler(&mockAuthService{}, jwtMgr)
		r.POST("/auth/credentials/setup", h.SetupPassword)

		body := SetupPasswordRequest{Token: "token_123", Password: "short"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/credentials/setup", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 on password < 8 chars, got %d", w.Code)
		}
	})

	t.Run("double-spend token attempt -> 400 Bad Request", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockAuthService{
			SetupPasswordFn: func(ctx context.Context, input service.SetupPasswordInput) (*service.TokenPair, error) {
				return nil, domain.ErrTokenAlreadyUsed
			},
		}

		h := NewAuthHandler(mockSvc, jwtMgr)
		r.POST("/auth/credentials/setup", h.SetupPassword)

		body := SetupPasswordRequest{Token: "already_used_token", Password: "validpassword123"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/credentials/setup", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 when token is already used, got %d", w.Code)
		}
	})

	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockAuthService{
			SetupPasswordFn: func(ctx context.Context, input service.SetupPasswordInput) (*service.TokenPair, error) {
				return &service.TokenPair{
					AccessToken:  "access_123",
					RefreshToken: "refresh_456",
					ExpiresIn:    900,
				}, nil
			},
		}

		h := NewAuthHandler(mockSvc, jwtMgr)
		r.POST("/auth/credentials/setup", h.SetupPassword)

		body := SetupPasswordRequest{Token: "valid_token", Password: "validpassword123"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/credentials/setup", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
	})
}

func TestAuthHandler_Login(t *testing.T) {
	jwtMgr, _ := crypto.NewJWTManager(generateTestPrivateKeyPEM(t))

	t.Run("invalid credentials -> 401 Unauthorized", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockAuthService{
			LoginFn: func(ctx context.Context, input service.LoginInput) (*service.LoginOutput, error) {
				return nil, domain.ErrInvalidCredentials
			},
		}

		h := NewAuthHandler(mockSvc, jwtMgr)
		r.POST("/auth/login", h.Login)

		body := LoginRequest{Email: "user@example.com", Password: "wrongpassword"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", w.Code)
		}
	})

	t.Run("no tenant membership -> 401 Unauthorized", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockAuthService{
			LoginFn: func(ctx context.Context, input service.LoginInput) (*service.LoginOutput, error) {
				return nil, domain.ErrNoTenantMembership
			},
		}

		h := NewAuthHandler(mockSvc, jwtMgr)
		r.POST("/auth/login", h.Login)

		body := LoginRequest{Email: "user@example.com", Password: "correctpassword"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", w.Code)
		}
	})

	t.Run("workspace selection -> 200 OK", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockAuthService{
			LoginFn: func(ctx context.Context, input service.LoginInput) (*service.LoginOutput, error) {
				return &service.LoginOutput{
					Status:        domain.LoginStatusSelectWorkspace,
					ExchangeToken: "exchange_ok",
					Workspaces: []service.WorkspaceInfo{
						{TenantID: "tenant-a"},
						{TenantID: "tenant-b"},
					},
				}, nil
			},
		}

		h := NewAuthHandler(mockSvc, jwtMgr)
		r.POST("/auth/login", h.Login)

		body := LoginRequest{Email: "user@example.com", Password: "correctpassword"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}

		var envelope struct {
			Data struct {
				RequiresWorkspace bool            `json:"requires_workspace"`
				ExchangeToken     string          `json:"exchange_token"`
				Workspaces        []WorkspaceInfo `json:"workspaces"`
				AccessToken       string          `json:"access_token"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("failed to unmarshal login response: %v", err)
		}
		if !envelope.Data.RequiresWorkspace {
			t.Errorf("expected requires_workspace=true, got %v", envelope.Data.RequiresWorkspace)
		}
		if envelope.Data.ExchangeToken != "exchange_ok" {
			t.Errorf("expected exchange_token=exchange_ok, got %q", envelope.Data.ExchangeToken)
		}
		if len(envelope.Data.Workspaces) != 2 || envelope.Data.Workspaces[0].TenantID != "tenant-a" {
			t.Errorf("unexpected workspaces: %+v", envelope.Data.Workspaces)
		}
		if envelope.Data.AccessToken != "" {
			t.Errorf("login response must not contain access_token, got %q", envelope.Data.AccessToken)
		}
	})
}

func TestAuthHandler_SelectTenant(t *testing.T) {
	jwtMgr, _ := crypto.NewJWTManager(generateTestPrivateKeyPEM(t))

	t.Run("success -> 200 OK with token pair", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockAuthService{
			SelectWorkspaceFn: func(ctx context.Context, input service.SelectWorkspaceInput) (*service.TokenPair, error) {
				return &service.TokenPair{
					AccessToken:  "access_ok",
					RefreshToken: "refresh_ok",
					ExpiresIn:    900,
				}, nil
			},
		}

		h := NewAuthHandler(mockSvc, jwtMgr)
		r.POST("/auth/select-tenant", h.SelectTenant)

		body := SelectTenantRequest{ExchangeToken: "exchange_ok", TenantID: "tenant-a"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/select-tenant", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}

		var envelope struct {
			Data struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
				ExpiresIn    int    `json:"expires_in"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("failed to unmarshal select-tenant response: %v", err)
		}
		if envelope.Data.AccessToken != "access_ok" || envelope.Data.RefreshToken != "refresh_ok" || envelope.Data.ExpiresIn != 900 {
			t.Errorf("unexpected token pair: %+v", envelope.Data)
		}
	})
}

func TestAuthHandler_Refresh(t *testing.T) {
	jwtMgr, _ := crypto.NewJWTManager(generateTestPrivateKeyPEM(t))

	t.Run("revoked token -> 401 Unauthorized", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockAuthService{
			RefreshTokenFn: func(ctx context.Context, input service.RefreshTokenInput) (*service.TokenPair, error) {
				return nil, domain.ErrTokenRevoked
			},
		}

		h := NewAuthHandler(mockSvc, jwtMgr)
		r.POST("/auth/refresh", h.Refresh)

		body := RefreshTokenRequest{RefreshToken: "revoked_token"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401 on revoked refresh token, got %d", w.Code)
		}
	})
}

func TestAuthHandler_Logout(t *testing.T) {
	jwtMgr, _ := crypto.NewJWTManager(generateTestPrivateKeyPEM(t))

	t.Run("internal server error", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockAuthService{
			LogoutFn: func(ctx context.Context, input service.LogoutInput) error {
				return errors.New("revoke error")
			},
		}

		h := NewAuthHandler(mockSvc, jwtMgr)
		r.POST("/auth/logout", h.Logout)

		body := LogoutRequest{RefreshToken: "token_err"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/logout", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d", w.Code)
		}
	})

	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockAuthService{
			LogoutFn: func(ctx context.Context, input service.LogoutInput) error {
				return nil
			},
		}

		h := NewAuthHandler(mockSvc, jwtMgr)
		r.POST("/auth/logout", h.Logout)

		body := LogoutRequest{RefreshToken: "valid_refresh"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/logout", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
	})
}

func TestAuthHandler_JWKS(t *testing.T) {
	jwtMgr, _ := crypto.NewJWTManager(generateTestPrivateKeyPEM(t))

	t.Run("returns 200 with application/json", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		h := NewAuthHandler(&mockAuthService{}, jwtMgr)
		r.GET("/.well-known/jwks.json", h.JWKS)

		req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
		if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", contentType)
		}

		var jwks crypto.JWKSResponse
		if err := json.Unmarshal(w.Body.Bytes(), &jwks); err != nil {
			t.Fatalf("failed to unmarshal JWKS response: %v", err)
		}
		if len(jwks.Keys) != 1 || jwks.Keys[0].Alg != "RS256" || jwks.Keys[0].Kty != "RSA" {
			t.Errorf("unexpected JWKS key payload: %+v", jwks)
		}
	})
}
