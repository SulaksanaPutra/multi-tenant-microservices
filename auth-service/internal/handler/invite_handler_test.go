package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"auth-service/internal/domain"
	"auth-service/internal/middleware"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockInvitationService struct {
	InviteUserFn func(ctx context.Context, input service.InviteUserInput) (*service.InviteUserOutput, error)
}

func (m *mockInvitationService) InviteUser(ctx context.Context, input service.InviteUserInput) (*service.InviteUserOutput, error) {
	if m.InviteUserFn != nil {
		return m.InviteUserFn(ctx, input)
	}
	return &service.InviteUserOutput{
		UserID: "usr_abc123",
		Email:  input.Email,
		RoleID: input.RoleID,
		Token:  "raw-token",
	}, nil
}

func TestInviteHandler_Invite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func() (*httptest.ResponseRecorder, *gin.Engine) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)
		return w, r
	}

	validBody := func() []byte {
		b, _ := json.Marshal(map[string]any{
			"email":   "invitee@example.com",
			"role_id": "role_001",
		})
		return b
	}

	t.Run("validation failure (missing email)", func(t *testing.T) {
		w, r := newCtx()
		h := NewInviteHandler(&mockInvitationService{})
		r.POST("/invite", h.Invite)

		reqBody, _ := json.Marshal(map[string]any{"role_id": "role_001"})
		req := httptest.NewRequest(http.MethodPost, "/invite", bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("missing tenant id -> 400", func(t *testing.T) {
		w, r := newCtx()
		h := NewInviteHandler(&mockInvitationService{})
		r.POST("/invite", func(c *gin.Context) {
			c.Set(middleware.ContextKeyTenantID, "")
			c.Set(middleware.ContextKeyUserID, "admin_001")
			h.Invite(c)
		})

		req := httptest.NewRequest(http.MethodPost, "/invite", bytes.NewBuffer(validBody()))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("role not found -> 404", func(t *testing.T) {
		w, r := newCtx()
		h := NewInviteHandler(&mockInvitationService{
			InviteUserFn: func(_ context.Context, _ service.InviteUserInput) (*service.InviteUserOutput, error) {
				return nil, domain.ErrRoleNotFound
			},
		})
		r.POST("/invite", func(c *gin.Context) {
			c.Set(middleware.ContextKeyTenantID, "tnt_001")
			c.Set(middleware.ContextKeyUserID, "admin_001")
			h.Invite(c)
		})

		req := httptest.NewRequest(http.MethodPost, "/invite", bytes.NewBuffer(validBody()))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", w.Code)
		}
	})

	t.Run("service failure -> 500", func(t *testing.T) {
		w, r := newCtx()
		h := NewInviteHandler(&mockInvitationService{
			InviteUserFn: func(_ context.Context, _ service.InviteUserInput) (*service.InviteUserOutput, error) {
				return nil, errors.New("db error")
			},
		})
		r.POST("/invite", func(c *gin.Context) {
			c.Set(middleware.ContextKeyTenantID, "tnt_001")
			c.Set(middleware.ContextKeyUserID, "admin_001")
			h.Invite(c)
		})

		req := httptest.NewRequest(http.MethodPost, "/invite", bytes.NewBuffer(validBody()))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d", w.Code)
		}
	})

	t.Run("success -> 201 with token", func(t *testing.T) {
		w, r := newCtx()
		h := NewInviteHandler(&mockInvitationService{})
		r.POST("/invite", func(c *gin.Context) {
			c.Set(middleware.ContextKeyTenantID, "tnt_001")
			c.Set(middleware.ContextKeyUserID, "admin_001")
			h.Invite(c)
		})

		req := httptest.NewRequest(http.MethodPost, "/invite", bytes.NewBuffer(validBody()))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected status 201, got %d", w.Code)
		}

		var resp struct {
			Data InviteUserResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Data.Token != "raw-token" {
			t.Errorf("expected token 'raw-token', got '%s'", resp.Data.Token)
		}
		if resp.Data.UserID != "usr_abc123" {
			t.Errorf("expected user_id 'usr_abc123', got '%s'", resp.Data.UserID)
		}
	})
}
