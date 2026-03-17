package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"notification-service/internal/domain"

	"github.com/gin-gonic/gin"
)

type mockNotificationService struct {
	listNotificationsFn func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error)
}

func (m *mockNotificationService) ListNotifications(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
	if m.listNotificationsFn != nil {
		return m.listNotificationsFn(ctx, tenantID)
	}
	return nil, nil
}

func TestNotificationHandler_ListNotifications(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := &mockNotificationService{
		listNotificationsFn: func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
			if tenantID != "ten_test123" {
				t.Fatalf("expected tenantID 'ten_test123', got '%s'", tenantID)
			}
			return []domain.NotificationLog{
				{
					ID:             1,
					UserID:         "usr_123",
					TenantID:       "ten_test123",
					RecipientEmail: "user@example.com",
					Subject:        "Welcome",
					Body:           "Welcome to the platform",
					Status:         "sent",
					CreatedAt:      time.Now(),
				},
			}, nil
		},
	}

	h := NewNotificationHandler(mockSvc)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenantID", "ten_test123")
		c.Next()
	})
	r.GET("/api/notifications", h.ListNotifications)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}
