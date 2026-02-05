package handler

import (
	"context"
	"net/http"
	"time"

	"notification-service/internal/domain"
	"notification-service/internal/httputil"

	"github.com/gin-gonic/gin"
)

type NotificationLogResponse struct {
	ID             int       `json:"id"`
	UserID         string    `json:"user_id"`
	TenantID       string    `json:"tenant_id"`
	RecipientEmail string    `json:"recipient_email"`
	Subject        string    `json:"subject"`
	Body           string    `json:"body"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// NotificationService is the consumer-side interface expected by NotificationHandler.
type NotificationService interface {
	ListNotifications(ctx context.Context, tenantID string) ([]domain.NotificationLog, error)
}

type NotificationHandler struct {
	notificationService NotificationService
}

func NewNotificationHandler(notificationService NotificationService) *NotificationHandler {
	return &NotificationHandler{notificationService: notificationService}
}

func (h *NotificationHandler) ListNotifications(c *gin.Context) {
	// tenantID is guaranteed to be set by RequireJWT middleware.
	tenantID := c.GetString("tenantID")
	logs, err := h.notificationService.ListNotifications(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "Failed to retrieve notifications: "+err.Error())
		return
	}

	resp := make([]NotificationLogResponse, len(logs))
	for i, l := range logs {
		resp[i] = NotificationLogResponse{
			ID:             l.ID,
			UserID:         l.UserID,
			TenantID:       l.TenantID,
			RecipientEmail: l.RecipientEmail,
			Subject:        l.Subject,
			Body:           l.Body,
			Status:         l.Status,
			CreatedAt:      l.CreatedAt,
		}
	}

	httputil.WriteSuccess(c, http.StatusOK, "Notifications retrieved successfully", resp)
}
