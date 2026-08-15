package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
	"notification-service/internal/domain"

	"github.com/gin-gonic/gin"
)

type NotificationLogResponse struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	TenantID    string    `json:"tenant_id"`
	Description string    `json:"description"`
	Body        string    `json:"body"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type NotificationHandler struct {
	notificationService NotificationService
}

func NewNotificationHandler(notificationService NotificationService) *NotificationHandler {
	return &NotificationHandler{notificationService: notificationService}
}

func (h *NotificationHandler) ListNotifications(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		httputil.WriteError(c, http.StatusUnauthorized, "notification handler: missing tenant_id in token claims")
		return
	}

	logs, err := h.notificationService.ListNotifications(c.Request.Context(), tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrTenantIDRequired) {
			httputil.WriteError(c, http.StatusBadRequest, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, "failed to retrieve notifications: "+err.Error())
		return
	}

	resp := make([]NotificationLogResponse, len(logs))
	for i, l := range logs {
		resp[i] = NotificationLogResponse{
			ID:          l.ID,
			UserID:      l.UserID,
			TenantID:    l.TenantID,
			Description: l.Description,
			Body:        l.Body,
			Status:      l.Status,
			CreatedAt:   l.CreatedAt,
			UpdatedAt:   l.UpdatedAt,
		}
	}

	httputil.WriteSuccess(c, http.StatusOK, "Notifications retrieved successfully", resp)
}
