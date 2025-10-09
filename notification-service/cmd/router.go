package main

import (
	"net/http"

	"notification-service/internal/handler"
	"notification-service/internal/utils"

	"github.com/gin-gonic/gin"
)

// newRouter initializes HTTP routes and health endpoints for Notification Service.
func newRouter(notifHandler *handler.NotificationHandler) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	r.GET("/health", func(c *gin.Context) {
		utils.WriteSuccess[any](c, http.StatusOK, "OK", nil)
	})

	r.GET("/api/notifications", notifHandler.GetNotifications)

	return r
}
