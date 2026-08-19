package main

import (
	"net/http"
	"os"

	"notification-service/internal/handler"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"

	"github.com/gin-gonic/gin"
)

func newRouter(notificationHandler *handler.NotificationHandler) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), gin.Logger())

	router.GET("/health", func(c *gin.Context) {
		httputil.WriteSuccess[any](c, http.StatusOK, "OK", nil)
	})

	publicKeyPEM := os.Getenv("AUTH_JWT_PUBLIC_KEY_PEM")
	if publicKeyPEM == "" {
		panic("notification-service: AUTH_JWT_PUBLIC_KEY_PEM environment variable is required")
	}

	authServiceURL := os.Getenv("AUTH_SERVICE_URL")
	internalToken := os.Getenv("INTERNAL_SERVICE_TOKEN")
	versionCache := middleware.NewVersionCache(authServiceURL, internalToken)

	router.GET("/api/notifications", middleware.RequireJWT(publicKeyPEM, middleware.WithVersionCache(versionCache)), middleware.RequirePermission("notifications:read"), notificationHandler.ListNotifications)

	return router
}
