package main

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"payment-service/internal/domain"
	"payment-service/internal/handler"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
)

// newRouter initializes HTTP endpoints, public webhooks, and JWT/permission middleware for payment-service.
func newRouter(paymentHandler *handler.PaymentHandler, jwtPubKeyPEM string) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "UP", "service": "payment-service"})
	})

	r.POST("/api/payments/webhook/:provider", paymentHandler.HandleWebhook)

	api := r.Group("/api/payments")
	api.Use(middleware.RequireJWT(jwtPubKeyPEM))
	{
		api.GET("/:id", middleware.RequirePermission(domain.PermissionPaymentsRead), paymentHandler.GetPaymentByID)
		api.GET("/by-order/:orderID", middleware.RequirePermission(domain.PermissionPaymentsRead), paymentHandler.GetPaymentByOrderID)
		api.PUT("/config", middleware.RequirePermission(domain.PermissionPaymentsManage), paymentHandler.UpdatePSPConfig)
		api.GET("/config", middleware.RequirePermission(domain.PermissionPaymentsManage), paymentHandler.GetPSPConfig)
	}

	return r
}
