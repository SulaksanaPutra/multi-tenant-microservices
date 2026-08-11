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

type mockInternalPermissionService struct {
	RegisterPermissionsFn      func(ctx context.Context, input service.InternalRegisterPermissionsInput) error
	GetUserPermissionVersionFn func(ctx context.Context, userID, tenantID string) (int64, error)
}

func (m *mockInternalPermissionService) RegisterPermissions(ctx context.Context, input service.InternalRegisterPermissionsInput) error {
	if m.RegisterPermissionsFn != nil {
		return m.RegisterPermissionsFn(ctx, input)
	}
	return nil
}

func (m *mockInternalPermissionService) GetUserPermissionVersion(ctx context.Context, userID, tenantID string) (int64, error) {
	if m.GetUserPermissionVersionFn != nil {
		return m.GetUserPermissionVersionFn(ctx, userID, tenantID)
	}
	return 1, nil
}

func TestInternalPermissionHandler_RegisterPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("validation failure (missing service)", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		h := NewInternalPermissionHandler(&mockInternalPermissionService{})
		r.POST("/internal/permissions/register", h.RegisterPermissions)

		reqBody := map[string]any{
			"service": "",
			"permissions": []map[string]string{
				{"name": "orders:write"},
			},
		}
		jsonBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/internal/permissions/register", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("service failure -> 500", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockInternalPermissionService{
			RegisterPermissionsFn: func(_ context.Context, _ service.InternalRegisterPermissionsInput) error {
				return errors.New("db error")
			},
		}
		h := NewInternalPermissionHandler(mockSvc)
		r.POST("/internal/permissions/register", h.RegisterPermissions)

		reqBody := InternalRegisterPermissionsRequest{
			Service: "order-service",
			Permissions: []InternalPermissionItemRequest{
				{Name: "orders:write", Description: "Create order"},
			},
		}
		jsonBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/internal/permissions/register", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d", w.Code)
		}
	})

	t.Run("success -> 200 OK", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockInternalPermissionService{
			RegisterPermissionsFn: func(_ context.Context, input service.InternalRegisterPermissionsInput) error {
				if input.Service != "order-service" || len(input.Permissions) != 1 {
					return errors.New("invalid input received")
				}
				return nil
			},
		}
		h := NewInternalPermissionHandler(mockSvc)
		r.POST("/internal/permissions/register", h.RegisterPermissions)

		reqBody := InternalRegisterPermissionsRequest{
			Service: "order-service",
			Permissions: []InternalPermissionItemRequest{
				{Name: "orders:write", Description: "Create order"},
			},
		}
		jsonBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/internal/permissions/register", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
	})
}

func TestInternalPermissionHandler_GetUserPermissionVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing tenant_id -> 400 Bad Request", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		h := NewInternalPermissionHandler(&mockInternalPermissionService{})
		r.GET("/internal/permissions/users/:user_id/version", h.GetUserPermissionVersion)

		req := httptest.NewRequest(http.MethodGet, "/internal/permissions/users/usr_123/version", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("success -> returns permission version", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockInternalPermissionService{
			GetUserPermissionVersionFn: func(_ context.Context, userID, tenantID string) (int64, error) {
				if userID == "usr_123" && tenantID == "tnt_001" {
					return 5, nil
				}
				return 1, nil
			},
		}
		h := NewInternalPermissionHandler(mockSvc)
		r.GET("/internal/permissions/users/:user_id/version", h.GetUserPermissionVersion)

		req := httptest.NewRequest(http.MethodGet, "/internal/permissions/users/usr_123/version?tenant_id=tnt_001", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}

		var resp httputil.StandardResponse[InternalPermissionVersionResponse]
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Data.PermVersion != 5 {
			t.Errorf("expected permission version 5, got %d", resp.Data.PermVersion)
		}
	})
}
