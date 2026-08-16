package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"user-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockUserService struct {
	listUsersFn   func(ctx context.Context, tenantID string) ([]service.UserOutput, error)
	getUserByIDFn func(ctx context.Context, userID string) (*service.UserOutput, error)
	updateUserFn  func(ctx context.Context, input service.UpdateUserInput) error
}

func (m *mockUserService) ListUsers(ctx context.Context, tenantID string) ([]service.UserOutput, error) {
	if m.listUsersFn != nil {
		return m.listUsersFn(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockUserService) GetUserByID(ctx context.Context, userID string) (*service.UserOutput, error) {
	if m.getUserByIDFn != nil {
		return m.getUserByIDFn(ctx, userID)
	}
	return &service.UserOutput{ID: userID, Email: "owner@example.com", Name: "Test User"}, nil
}

func (m *mockUserService) UpdateUser(ctx context.Context, input service.UpdateUserInput) error {
	if m.updateUserFn != nil {
		return m.updateUserFn(ctx, input)
	}
	return nil
}

func setupTestRouter(userHandler *UserHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "usr_test123")
		c.Set("tenantID", "ten_test123")
		c.Next()
	})
	router.GET("/api/users", userHandler.ListUsers)
	router.GET("/api/users/me", userHandler.GetMe)
	router.PUT("/api/users/me", userHandler.UpdateMe)
	return router
}

func TestUserHandler_ListUsers(t *testing.T) {
	var gotTenantID string
	mockUserService := &mockUserService{
		listUsersFn: func(ctx context.Context, tenantID string) ([]service.UserOutput, error) {
			gotTenantID = tenantID
			return []service.UserOutput{
				{ID: "usr_1", Email: "test1@example.com", Name: "Test One"},
			}, nil
		},
	}
	userHandler := NewUserHandler(mockUserService)
	router := setupTestRouter(userHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if gotTenantID != "ten_test123" {
		t.Fatalf("expected ListUsers to be scoped to ten_test123, got %q", gotTenantID)
	}
}

func TestUserHandler_UpdateMe(t *testing.T) {
	updated := false
	mockUserService := &mockUserService{
		updateUserFn: func(ctx context.Context, input service.UpdateUserInput) error {
			if input.UserID == "usr_test123" && input.Name == "Jane Doe" {
				updated = true
			}
			return nil
		},
	}
	userHandler := NewUserHandler(mockUserService)
	router := setupTestRouter(userHandler)

	body, _ := json.Marshal(UpdateUserRequest{Name: "Jane Doe"})
	req := httptest.NewRequest(http.MethodPut, "/api/users/me", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !updated {
		t.Fatalf("expected UpdateUser to be called")
	}
}
