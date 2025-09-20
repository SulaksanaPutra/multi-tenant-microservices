package main

import (
	"net/http"

	"order-service/internal/handler"
	"order-service/internal/service"
	"order-service/internal/utils"

	"github.com/gin-gonic/gin"
)

// newRouter initializes all HTTP routes for order-service.
//
// Public API:
//   GET /api/orders   — Fetch tenant-scoped orders (header: X-Tenant-ID)
func newRouter(orderService service.OrderService) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	orderHandler := handler.NewOrderHandler(orderService)

	r.GET("/api/orders", orderHandler.GetOrders)
	r.GET("/health", func(c *gin.Context) {
		utils.WriteSuccess[any](c, http.StatusOK, "OK", nil)
	})

	return r
}
