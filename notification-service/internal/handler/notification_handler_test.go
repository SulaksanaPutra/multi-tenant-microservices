package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"notification-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockNotificationService struct {
	listNotificationsFn func(ctx context.Context, tenantID string) ([]service.NotificationLogOutput, error)
}

func (m *mockNotificationService) ListNotifications(ctx context.Context, tenantID string) ([]service.NotificationLogOutput, error) {
	if m.listNotificationsFn != nil {
		return m.listNotificationsFn(ctx, tenantID)
	}
	return nil, nil
}

func TestNotificationHandler_ListNotifications(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockNotificationService := &mockNotificationService{
		listNotificationsFn: func(ctx context.Context, tenantID string) ([]service.NotificationLogOutput, error) {
			if tenantID != "ten_test123" {
				t.Fatalf("expected tenantID 'ten_test123', got '%s'", tenantID)
			}
			return []service.NotificationLogOutput{
				{
					ID:          "ntf_1",
					UserID:      "usr_123",
					TenantID:    "ten_test123",
					Description: "Welcome",
					Body:        "Welcome to the platform",
					Status:      "sent",
					CreatedAt:   time.Now(),
				},
			}, nil
		},
	}

	notificationHandler := NewNotificationHandler(mockNotificationService)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenantID", "ten_test123")
		c.Next()
	})
	router.GET("/api/notifications", notificationHandler.ListNotifications)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}
