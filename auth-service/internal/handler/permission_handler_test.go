package handler

import (
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

type mockPermissionService struct {
	ListPermissionsFn func(ctx context.Context) ([]service.PermissionOutput, error)
}

func (m *mockPermissionService) ListPermissions(ctx context.Context) ([]service.PermissionOutput, error) {
	if m.ListPermissionsFn != nil {
		return m.ListPermissionsFn(ctx)
	}
	return nil, nil
}

func TestPermissionHandler_ListPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("service failure -> 500", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, router := gin.CreateTestContext(w)

		mockPermissionService := &mockPermissionService{
			ListPermissionsFn: func(_ context.Context) ([]service.PermissionOutput, error) {
				return nil, errors.New("list failure")
			},
		}
		permissionHandler := NewPermissionHandler(mockPermissionService)
		router.GET("/api/auth/permissions", permissionHandler.ListPermissions)

		req := httptest.NewRequest(http.MethodGet, "/api/auth/permissions", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d", w.Code)
		}
	})

	t.Run("success -> returns permissions list", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, router := gin.CreateTestContext(w)

		mockPermissionService := &mockPermissionService{
			ListPermissionsFn: func(_ context.Context) ([]service.PermissionOutput, error) {
				return []service.PermissionOutput{
					{ID: "p1", Name: "orders:read", Service: "order-service"},
				}, nil
			},
		}
		permissionHandler := NewPermissionHandler(mockPermissionService)
		router.GET("/api/auth/permissions", permissionHandler.ListPermissions)

		req := httptest.NewRequest(http.MethodGet, "/api/auth/permissions", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}

		var resp httputil.StandardResponse[[]PermissionResponse]
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if len(resp.Data) != 1 || resp.Data[0].Name != "orders:read" {
			t.Errorf("unexpected data in response: %v", resp.Data)
		}
	})
}
