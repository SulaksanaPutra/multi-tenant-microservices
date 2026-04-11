package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"auth-service/internal/domain"
	"auth-service/internal/httputil"
	"auth-service/internal/middleware"
	"auth-service/internal/repository"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockRoleAppService struct {
	CreateRoleFn             func(ctx context.Context, input service.CreateRoleInput) (*domain.Role, error)
	GetRoleFn                func(ctx context.Context, roleID string) (*domain.Role, error)
	ListRolesForTenantFn     func(ctx context.Context, tenantID string) ([]domain.Role, error)
	UpdateRolePermissionsFn  func(ctx context.Context, input service.UpdateRolePermissionsInput) error
	DeleteRoleFn             func(ctx context.Context, roleID string) error
	AssignUserRoleFn         func(ctx context.Context, input service.AssignUserRoleInput) error
	GetUserRoleFn            func(ctx context.Context, userID, tenantID string) (*domain.UserRole, error)
	ListUserRolesForTenantFn func(ctx context.Context, tenantID string, userIDs []string) ([]repository.UserRoleBrief, error)
}

func (m *mockRoleAppService) CreateRole(ctx context.Context, input service.CreateRoleInput) (*domain.Role, error) {
	if m.CreateRoleFn != nil {
		return m.CreateRoleFn(ctx, input)
	}
	return &domain.Role{ID: "role_1", Name: input.Name}, nil
}

func (m *mockRoleAppService) GetRole(ctx context.Context, roleID string) (*domain.Role, error) {
	if m.GetRoleFn != nil {
		return m.GetRoleFn(ctx, roleID)
	}
	return &domain.Role{ID: roleID, Name: "role_name"}, nil
}

func (m *mockRoleAppService) ListRolesForTenant(ctx context.Context, tenantID string) ([]domain.Role, error) {
	if m.ListRolesForTenantFn != nil {
		return m.ListRolesForTenantFn(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockRoleAppService) UpdateRolePermissions(ctx context.Context, input service.UpdateRolePermissionsInput) error {
	if m.UpdateRolePermissionsFn != nil {
		return m.UpdateRolePermissionsFn(ctx, input)
	}
	return nil
}

func (m *mockRoleAppService) DeleteRole(ctx context.Context, roleID string) error {
	if m.DeleteRoleFn != nil {
		return m.DeleteRoleFn(ctx, roleID)
	}
	return nil
}

func (m *mockRoleAppService) AssignUserRole(ctx context.Context, input service.AssignUserRoleInput) error {
	if m.AssignUserRoleFn != nil {
		return m.AssignUserRoleFn(ctx, input)
	}
	return nil
}

func (m *mockRoleAppService) GetUserRole(ctx context.Context, userID, tenantID string) (*domain.UserRole, error) {
	if m.GetUserRoleFn != nil {
		return m.GetUserRoleFn(ctx, userID, tenantID)
	}
	return &domain.UserRole{UserID: userID, TenantID: tenantID, RoleID: "role_1"}, nil
}

func (m *mockRoleAppService) ListUserRolesForTenant(ctx context.Context, tenantID string, userIDs []string) ([]repository.UserRoleBrief, error) {
	if m.ListUserRolesForTenantFn != nil {
		return m.ListUserRolesForTenantFn(ctx, tenantID, userIDs)
	}
	return nil, nil
}

func TestRoleHandler_CreateRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing tenant_id -> 400 Bad Request", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		h := NewRoleHandler(&mockRoleAppService{})
		r.POST("/roles", h.CreateRole)

		reqBody := CreateRoleRequest{Name: "custom_role"}
		jsonBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/roles", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("role already exists -> 409 Conflict", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockRoleAppService{
			CreateRoleFn: func(_ context.Context, _ service.CreateRoleInput) (*domain.Role, error) {
				return nil, domain.ErrRoleAlreadyExists
			},
		}
		h := NewRoleHandler(mockSvc)
		r.POST("/roles", h.CreateRole)

		reqBody := CreateRoleRequest{TenantID: "tnt_001", Name: "custom_role"}
		jsonBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/roles", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected status 409, got %d", w.Code)
		}
	})

	t.Run("success -> 201 Created", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockRoleAppService{
			CreateRoleFn: func(_ context.Context, input service.CreateRoleInput) (*domain.Role, error) {
				return &domain.Role{ID: "role_123", Name: input.Name}, nil
			},
		}
		h := NewRoleHandler(mockSvc)
		r.POST("/roles", func(c *gin.Context) {
			c.Set(middleware.ContextKeyTenantID, "tnt_001")
			h.CreateRole(c)
		})

		reqBody := CreateRoleRequest{Name: "custom_role", PermissionIDs: []string{"p1"}}
		jsonBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/roles", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected status 201, got %d", w.Code)
		}

		var resp httputil.StandardResponse[domain.Role]
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Data.ID != "role_123" {
			t.Errorf("expected role ID 'role_123', got '%s'", resp.Data.ID)
		}
	})
}

func TestRoleHandler_GetRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("not found -> 404 Not Found", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockRoleAppService{
			GetRoleFn: func(_ context.Context, _ string) (*domain.Role, error) {
				return nil, domain.ErrRoleNotFound
			},
		}
		h := NewRoleHandler(mockSvc)
		r.GET("/roles/:id", h.GetRole)

		req := httptest.NewRequest(http.MethodGet, "/roles/non_existent", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", w.Code)
		}
	})

	t.Run("success -> 200 OK", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockRoleAppService{
			GetRoleFn: func(_ context.Context, roleID string) (*domain.Role, error) {
				return &domain.Role{ID: roleID, Name: "manager"}, nil
			},
		}
		h := NewRoleHandler(mockSvc)
		r.GET("/roles/:id", h.GetRole)

		req := httptest.NewRequest(http.MethodGet, "/roles/role_123", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
	})
}

func TestRoleHandler_UpdateRolePermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("system role protected -> 403 Forbidden", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockRoleAppService{
			UpdateRolePermissionsFn: func(_ context.Context, _ service.UpdateRolePermissionsInput) error {
				return domain.ErrSystemRoleProtected
			},
		}
		h := NewRoleHandler(mockSvc)
		r.PUT("/roles/:id/permissions", h.UpdateRolePermissions)

		reqBody := UpdateRolePermissionsRequest{PermissionIDs: []string{"p1"}}
		jsonBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPut, "/roles/sys_admin/permissions", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", w.Code)
		}
	})

	t.Run("success -> 200 OK", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		h := NewRoleHandler(&mockRoleAppService{})
		r.PUT("/roles/:id/permissions", h.UpdateRolePermissions)

		reqBody := UpdateRolePermissionsRequest{PermissionIDs: []string{"p1", "p2"}}
		jsonBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPut, "/roles/role_123/permissions", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
	})
}

func TestRoleHandler_DeleteRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("system role protected -> 403 Forbidden", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockRoleAppService{
			DeleteRoleFn: func(_ context.Context, _ string) error {
				return domain.ErrSystemRoleProtected
			},
		}
		h := NewRoleHandler(mockSvc)
		r.DELETE("/roles/:id", h.DeleteRole)

		req := httptest.NewRequest(http.MethodDelete, "/roles/role_sys", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", w.Code)
		}
	})

	t.Run("success -> 200 OK", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		h := NewRoleHandler(&mockRoleAppService{})
		r.DELETE("/roles/:id", h.DeleteRole)

		req := httptest.NewRequest(http.MethodDelete, "/roles/role_custom", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
	})
}

func TestRoleHandler_AssignUserRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("role not found -> 404 Not Found", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		mockSvc := &mockRoleAppService{
			AssignUserRoleFn: func(_ context.Context, _ service.AssignUserRoleInput) error {
				return domain.ErrRoleNotFound
			},
		}
		h := NewRoleHandler(mockSvc)
		r.POST("/users/:userID/role", func(c *gin.Context) {
			c.Set(middleware.ContextKeyTenantID, "tnt_001")
			h.AssignUserRole(c)
		})

		reqBody := AssignUserRoleRequest{RoleID: "role_invalid"}
		jsonBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/users/usr_100/role", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", w.Code)
		}
	})

	t.Run("success -> 200 OK", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		h := NewRoleHandler(&mockRoleAppService{})
		r.POST("/users/:userID/role", func(c *gin.Context) {
			c.Set(middleware.ContextKeyTenantID, "tnt_001")
			h.AssignUserRole(c)
		})

		reqBody := AssignUserRoleRequest{RoleID: "role_valid"}
		jsonBytes, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/users/usr_100/role", bytes.NewBuffer(jsonBytes))
		req.Header.Set("Content-Type", "application/json")

		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
	})
}
