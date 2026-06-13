package main

import (
	"net/http"
	"os"

	"order-service/internal/handler"
	"order-service/internal/httputil"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/middleware"

	"github.com/gin-gonic/gin"
)

func newRouter(tenantDBResolver *tenantdb.Resolver) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	r.GET("/health", func(c *gin.Context) {
		httputil.WriteSuccess[any](c, http.StatusOK, "OK", nil)
	})

	publicKeyPEM := os.Getenv("AUTH_JWT_PUBLIC_KEY_PEM")
	if publicKeyPEM == "" {
		panic("order-service: AUTH_JWT_PUBLIC_KEY_PEM environment variable is required")
	}

	authServiceURL := os.Getenv("AUTH_SERVICE_URL")
	internalToken := os.Getenv("INTERNAL_SERVICE_TOKEN")
	versionCache := middleware.NewVersionCache(authServiceURL, internalToken)

	orderHandler := handler.NewOrderHandler(nil)

	api := r.Group("/api")
	api.Use(middleware.RequireJWT(publicKeyPEM, tenantDBResolver, versionCache))

	api.GET("/orders", middleware.RequirePermission("orders:read"), orderHandler.ListOrders)
	api.POST("/orders", middleware.RequirePermission("orders:write"), orderHandler.CreateOrder)

	return r
}
