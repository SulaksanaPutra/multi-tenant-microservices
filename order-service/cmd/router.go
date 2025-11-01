package main

import (
	"net/http"

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

	orderHandler := handler.NewOrderHandler(nil)

	api := r.Group("/api")
	api.Use(middleware.RequireTenantHeader(tenantDBResolver))

	api.GET("/orders", orderHandler.ListOrders)
	api.POST("/orders", orderHandler.CreateOrder)

	return r
}
