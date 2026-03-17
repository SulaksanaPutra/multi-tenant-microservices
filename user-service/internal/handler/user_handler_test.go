package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/authclient"
	"user-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockUserService struct {
	listUsersFn   func(ctx context.Context) ([]domain.User, error)
	updateUserFn  func(ctx context.Context, input service.UpdateUserServiceInput) error
	getUserByIDFn func(ctx context.Context, userID string) (*domain.User, error)
}

func (m *mockUserService) ListUsers(ctx context.Context) ([]domain.User, error) {
	if m.listUsersFn != nil {
		return m.listUsersFn(ctx)
	}
	return nil, nil
}

func (m *mockUserService) UpdateUser(ctx context.Context, input service.UpdateUserServiceInput) error {
	if m.updateUserFn != nil {
		return m.updateUserFn(ctx, input)
	}
	return nil
}

func (m *mockUserService) GetUserByID(ctx context.Context, userID string) (*domain.User, error) {
	if m.getUserByIDFn != nil {
		return m.getUserByIDFn(ctx, userID)
	}
	return nil, nil
}

type mockAuthClient struct {
	assignUserRoleFn  func(ctx context.Context, authToken, userID, roleID string) (*authclient.UserRoleResponse, error)
	getUserRoleFn     func(ctx context.Context, authToken, userID string) (*authclient.UserRoleResponse, error)
	createRoleFn      func(ctx context.Context, authToken string, input authclient.CreateRoleInput) (*authclient.Role, error)
	listRolesFn       func(ctx context.Context, authToken string) ([]authclient.Role, error)
	listPermissionsFn func(ctx context.Context, authToken string) ([]authclient.PermissionCatalogItem, error)
}

func (m *mockAuthClient) AssignUserRole(ctx context.Context, authToken, userID, roleID string) (*authclient.UserRoleResponse, error) {
	if m.assignUserRoleFn != nil {
		return m.assignUserRoleFn(ctx, authToken, userID, roleID)
	}
	return nil, nil
}

func (m *mockAuthClient) GetUserRole(ctx context.Context, authToken, userID string) (*authclient.UserRoleResponse, error) {
	if m.getUserRoleFn != nil {
		return m.getUserRoleFn(ctx, authToken, userID)
	}
	return nil, nil
}

func (m *mockAuthClient) CreateRole(ctx context.Context, authToken string, input authclient.CreateRoleInput) (*authclient.Role, error) {
	if m.createRoleFn != nil {
		return m.createRoleFn(ctx, authToken, input)
	}
	return nil, nil
}

func (m *mockAuthClient) ListRoles(ctx context.Context, authToken string) ([]authclient.Role, error) {
	if m.listRolesFn != nil {
		return m.listRolesFn(ctx, authToken)
	}
	return nil, nil
}

func (m *mockAuthClient) ListPermissions(ctx context.Context, authToken string) ([]authclient.PermissionCatalogItem, error) {
	if m.listPermissionsFn != nil {
		return m.listPermissionsFn(ctx, authToken)
	}
	return nil, nil
}

func setupTestRouter(userHandler *UserHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "usr_test123")
		c.Set("tenantID", "ten_test123")
		c.Next()
	})
	r.GET("/api/users", userHandler.ListUsers)
	r.PUT("/api/users/me", userHandler.UpdateMe)
	r.GET("/api/users/:user_id/role", userHandler.GetUserRole)
	r.PUT("/api/users/:user_id/role", userHandler.AssignUserRole)
	r.POST("/api/users/roles", userHandler.CreateRole)
	r.GET("/api/users/roles", userHandler.ListRoles)
	r.GET("/api/users/permissions", userHandler.ListPermissions)
	return r
}

func TestUserHandler_ListUsers(t *testing.T) {
	mockSvc := &mockUserService{
		listUsersFn: func(ctx context.Context) ([]domain.User, error) {
			return []domain.User{
				{ID: "usr_1", Email: "test1@example.com", Name: "Test One"},
			}, nil
		},
	}
	userHandler := NewUserHandler(mockSvc, &mockAuthClient{})
	r := setupTestRouter(userHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestUserHandler_UpdateMe(t *testing.T) {
	updated := false
	mockSvc := &mockUserService{
		updateUserFn: func(ctx context.Context, input service.UpdateUserServiceInput) error {
			if input.UserID == "usr_test123" && input.Name == "Jane Doe" {
				updated = true
			}
			return nil
		},
	}
	userHandler := NewUserHandler(mockSvc, &mockAuthClient{})
	r := setupTestRouter(userHandler)

	body, _ := json.Marshal(UpdateUserRequest{Name: "Jane Doe"})
	req := httptest.NewRequest(http.MethodPut, "/api/users/me", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !updated {
		t.Fatalf("expected UpdateUser to be called")
	}
}

func TestUserHandler_AssignUserRole(t *testing.T) {
	assigned := false
	mockAuth := &mockAuthClient{
		assignUserRoleFn: func(ctx context.Context, authToken, userID, roleID string) (*authclient.UserRoleResponse, error) {
			if userID == "usr_456" && roleID == "role_admin" {
				assigned = true
			}
			return &authclient.UserRoleResponse{UserID: userID, RoleID: roleID}, nil
		},
	}
	userHandler := NewUserHandler(&mockUserService{}, mockAuth)
	r := setupTestRouter(userHandler)

	body, _ := json.Marshal(AssignRoleRequest{RoleID: "role_admin"})
	req := httptest.NewRequest(http.MethodPut, "/api/users/usr_456/role", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !assigned {
		t.Fatalf("expected AssignUserRole to be called with correct arguments")
	}
}

func TestUserHandler_GetUserRole(t *testing.T) {
	mockAuth := &mockAuthClient{
		getUserRoleFn: func(ctx context.Context, authToken, userID string) (*authclient.UserRoleResponse, error) {
			return &authclient.UserRoleResponse{UserID: userID, RoleName: "admin"}, nil
		},
	}
	userHandler := NewUserHandler(&mockUserService{}, mockAuth)
	r := setupTestRouter(userHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/users/usr_789/role", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestUserHandler_CreateRole(t *testing.T) {
	created := false
	mockAuth := &mockAuthClient{
		createRoleFn: func(ctx context.Context, authToken string, input authclient.CreateRoleInput) (*authclient.Role, error) {
			if input.Name == "editor" {
				created = true
			}
			return &authclient.Role{ID: "role_999", Name: "editor"}, nil
		},
	}
	userHandler := NewUserHandler(&mockUserService{}, mockAuth)
	r := setupTestRouter(userHandler)

	body, _ := json.Marshal(CreateRoleRequest{Name: "editor", Description: "Editor role", Permissions: []string{"users:read"}})
	req := httptest.NewRequest(http.MethodPost, "/api/users/roles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}
	if !created {
		t.Error("expected CreateRole to be called")
	}
}

func TestUserHandler_ListRoles(t *testing.T) {
	mockAuth := &mockAuthClient{
		listRolesFn: func(ctx context.Context, authToken string) ([]authclient.Role, error) {
			return []authclient.Role{{ID: "r1", Name: "admin"}}, nil
		},
	}
	userHandler := NewUserHandler(&mockUserService{}, mockAuth)
	r := setupTestRouter(userHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/users/roles", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestUserHandler_ListPermissions(t *testing.T) {
	mockAuth := &mockAuthClient{
		listPermissionsFn: func(ctx context.Context, authToken string) ([]authclient.PermissionCatalogItem, error) {
			return []authclient.PermissionCatalogItem{{ID: "p1", Name: "users:read"}}, nil
		},
	}
	userHandler := NewUserHandler(&mockUserService{}, mockAuth)
	r := setupTestRouter(userHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/users/permissions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}
