package main

import (
	"net/http"
	"os"

	"notification-service/internal/handler"
	"notification-service/internal/httputil"
	"notification-service/internal/middleware"

	"github.com/gin-gonic/gin"
)

// newRouter initializes HTTP routes and health endpoints for Notification Service.
func newRouter(notifHandler *handler.NotificationHandler) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	r.GET("/health", func(c *gin.Context) {
		httputil.WriteSuccess[any](c, http.StatusOK, "OK", nil)
	})

	publicKeyPEM := os.Getenv("AUTH_JWT_PUBLIC_KEY_PEM")
	if publicKeyPEM == "" {
		panic("notification-service: AUTH_JWT_PUBLIC_KEY_PEM environment variable is required")
	}

	r.GET("/api/notifications", middleware.RequireJWT(publicKeyPEM), notifHandler.ListNotifications)

	return r
}
