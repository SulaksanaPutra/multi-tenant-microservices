package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"user-service/internal/domain"
	"user-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockUserService struct {
	listUsersFn  func(ctx context.Context, tenantID string) ([]domain.User, error)
	updateUserFn func(ctx context.Context, input service.UpdateUserServiceInput) error
}

func (m *mockUserService) ListUsers(ctx context.Context, tenantID string) ([]domain.User, error) {
	if m.listUsersFn != nil {
		return m.listUsersFn(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockUserService) UpdateUser(ctx context.Context, input service.UpdateUserServiceInput) error {
	if m.updateUserFn != nil {
		return m.updateUserFn(ctx, input)
	}
	return nil
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
	return r
}

func TestUserHandler_ListUsers(t *testing.T) {
	var gotTenantID string
	mockSvc := &mockUserService{
		listUsersFn: func(ctx context.Context, tenantID string) ([]domain.User, error) {
			gotTenantID = tenantID
			return []domain.User{
				{ID: "usr_1", Email: "test1@example.com", Name: "Test One"},
			}, nil
		},
	}
	userHandler := NewUserHandler(mockSvc)
	r := setupTestRouter(userHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if gotTenantID != "ten_test123" {
		t.Fatalf("expected ListUsers to be scoped to ten_test123, got %q", gotTenantID)
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
	userHandler := NewUserHandler(mockSvc)
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
