package main

import (
	"net/http"
	"os"

	"user-service/internal/handler"
	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"

	"github.com/gin-gonic/gin"
)

// newRouter initializes all HTTP routes, middleware, and handlers for User Service.
func newRouter(userHandler *handler.UserHandler) http.Handler {
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
		api.Use(middleware.RequireJWT(publicKeyPEM, middleware.WithVersionCache(versionCache)))

		api.GET("/users", middleware.RequirePermission("users:read"), userHandler.ListUsers)
		api.GET("/users/me", middleware.RequirePermission("users:read"), userHandler.GetMe)
		api.PUT("/users/me", middleware.RequirePermission("users:write"), userHandler.UpdateMe)
	}

	return r
}
