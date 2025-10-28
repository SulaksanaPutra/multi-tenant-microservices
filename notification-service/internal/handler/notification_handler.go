package handler

import (
	"context"
	"net/http"

	"notification-service/internal/httputil"
	"notification-service/internal/repository"

	"github.com/gin-gonic/gin"
)

// NotificationService is the consumer-side interface expected by NotificationHandler.
type NotificationService interface {
	ListNotifications(ctx context.Context, tenantID string) ([]repository.NotificationLog, error)
}

type NotificationHandler struct {
	svc NotificationService
}

func NewNotificationHandler(svc NotificationService) *NotificationHandler {
	return &NotificationHandler{svc: svc}
}

func (h *NotificationHandler) ListNotifications(c *gin.Context) {
	tenantID := c.GetHeader("X-Tenant-ID")
	if tenantID == "" {
		tenantID = c.Query("tenant_id")
	}
	logs, err := h.svc.ListNotifications(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "Failed to retrieve notifications: "+err.Error())
		return
	}
	httputil.WriteSuccess(c, http.StatusOK, "Notifications retrieved successfully", logs)
}
