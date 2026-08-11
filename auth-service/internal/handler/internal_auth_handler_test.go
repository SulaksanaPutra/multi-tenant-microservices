package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockInternalAuthService struct {
	CreatePasswordSetupTokenFn func(ctx context.Context, input service.InternalCreateSetupTokenInput) (string, error)
}

func (m *mockInternalAuthService) CreatePasswordSetupToken(ctx context.Context, input service.InternalCreateSetupTokenInput) (string, error) {
	if m.CreatePasswordSetupTokenFn != nil {
		return m.CreatePasswordSetupTokenFn(ctx, input)
	}
	return "", nil
}

func TestInternalAuthHandler_CreateSetupToken(t *testing.T) {
	t.Run("validation failure (missing email)", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		h := NewInternalAuthHandler(&mockInternalAuthService{})
		r.POST("/internal/auth/setup-token", h.CreateSetupToken)

		body := InternalCreateSetupTokenRequest{UserID: "usr_1", TenantID: "tnt_1", Email: ""}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/internal/auth/setup-token", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 on missing email, got %d", w.Code)
		}
	})

	t.Run("internal error", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockInternalAuthService{
			CreatePasswordSetupTokenFn: func(ctx context.Context, input service.InternalCreateSetupTokenInput) (string, error) {
				return "", errors.New("db error")
			},
		}

		h := NewInternalAuthHandler(mockSvc)
		r.POST("/internal/auth/setup-token", h.CreateSetupToken)

		body := InternalCreateSetupTokenRequest{UserID: "usr_1", TenantID: "tnt_1", Email: "user@example.com"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/internal/auth/setup-token", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500 on internal error, got %d", w.Code)
		}
	})

	t.Run("success -> 200 OK", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockInternalAuthService{
			CreatePasswordSetupTokenFn: func(ctx context.Context, input service.InternalCreateSetupTokenInput) (string, error) {
				return "setup_token_xyz", nil
			},
		}

		h := NewInternalAuthHandler(mockSvc)
		r.POST("/internal/auth/setup-token", h.CreateSetupToken)

		body := InternalCreateSetupTokenRequest{UserID: "usr_1", TenantID: "tnt_1", Email: "user@example.com"}
		jsonBytes, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/internal/auth/setup-token", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}

		var resp httputil.StandardResponse[InternalCreateSetupTokenResponse]
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Data.Token != "setup_token_xyz" {
			t.Errorf("expected token 'setup_token_xyz', got '%s'", resp.Data.Token)
		}
	})
}
