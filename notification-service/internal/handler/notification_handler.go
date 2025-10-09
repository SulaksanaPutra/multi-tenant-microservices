package handler

import (
	"net/http"

	"notification-service/internal/service"
	"notification-service/internal/utils"

	"github.com/gin-gonic/gin"
)

type NotificationHandler struct {
	svc service.NotificationService
}

func NewNotificationHandler(svc service.NotificationService) *NotificationHandler {
	return &NotificationHandler{svc: svc}
}

func (h *NotificationHandler) GetNotifications(c *gin.Context) {
	tenantID := c.GetHeader("X-Tenant-ID")
	if tenantID == "" {
		tenantID = c.Query("tenant_id")
	}
	logs, err := h.svc.GetNotifications(c.Request.Context(), tenantID)
	if err != nil {
		utils.WriteError(c, http.StatusInternalServerError, "Failed to retrieve notifications: "+err.Error())
		return
	}
	utils.WriteSuccess(c, http.StatusOK, "Notifications retrieved successfully", logs)
}
