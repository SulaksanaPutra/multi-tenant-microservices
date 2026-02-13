package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"notification-service/internal/domain"
	"notification-service/internal/httputil"

	"github.com/gin-gonic/gin"
)

type mockNotificationService struct {
	listNotificationsFunc func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error)
}

func (m *mockNotificationService) ListNotifications(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
	if m.listNotificationsFunc != nil {
		return m.listNotificationsFunc(ctx, tenantID)
	}
	return nil, nil
}

func TestListNotifications_Success(t *testing.T) {
	svc := &mockNotificationService{
		listNotificationsFunc: func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
			return []domain.NotificationLog{
				{
					ID:             1,
					UserID:         "usr_123",
					TenantID:       tenantID,
					RecipientEmail: "owner@company.com",
					Subject:        "Welcome",
					Status:         "sent",
					CreatedAt:      time.Now(),
				},
			}, nil
		},
	}

	h := NewNotificationHandler(svc)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	c.Request = req
	c.Set("tenantID", "tenant_abc")

	h.ListNotifications(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp httputil.StandardResponse[[]NotificationLogResponse]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.Data) != 1 || resp.Data[0].TenantID != "tenant_abc" {
		t.Errorf("unexpected response content: %+v", resp.Data)
	}
}

func TestListNotifications_ServiceError(t *testing.T) {
	svc := &mockNotificationService{
		listNotificationsFunc: func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
			return nil, errors.New("db error")
		},
	}

	h := NewNotificationHandler(svc)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	c.Request = req

	h.ListNotifications(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}
