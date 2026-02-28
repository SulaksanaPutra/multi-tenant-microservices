package main

import (
	"net/http"
	"os"

	"user-service/internal/httputil"
	"user-service/internal/middleware"

	"github.com/gin-gonic/gin"
)

// newRouter initializes all HTTP routes, middleware, and handlers for User Service.
func newRouter() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	r.GET("/health", func(c *gin.Context) {
		httputil.WriteSuccess[any](c, http.StatusOK, "OK", nil)
	})

	publicKeyPEM := os.Getenv("AUTH_JWT_PUBLIC_KEY_PEM")
	if publicKeyPEM != "" {
		authServiceURL := os.Getenv("AUTH_SERVICE_URL")
		internalToken := os.Getenv("INTERNAL_SERVICE_TOKEN")
		versionCache := middleware.NewVersionCache(authServiceURL, internalToken)

		api := r.Group("/api")
		api.Use(middleware.RequireJWT(publicKeyPEM, versionCache))
		api.GET("/users/me", middleware.RequirePermission("users:read"), func(c *gin.Context) {
			userID := c.GetString(middleware.ContextKeyUserID)
			tenantID := c.GetString(middleware.ContextKeyTenantID)
			email := c.GetString(middleware.ContextKeyEmail)
			httputil.WriteSuccess[any](c, http.StatusOK, "User Profile Fetched", gin.H{
				"user_id":   userID,
				"tenant_id": tenantID,
				"email":     email,
			})
		})
	}

	return r
}
