package main

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"payment-service/internal/domain"
	"payment-service/internal/handler"

	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
)

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
		api.GET("/methods", middleware.RequirePermission(domain.PermissionPaymentsRead), paymentHandler.GetAvailablePaymentMethods)
		api.POST("/initiate", middleware.RequirePermission(domain.PermissionPaymentsCreate), paymentHandler.InitiatePayment)
		api.GET("/:id", middleware.RequirePermission(domain.PermissionPaymentsRead), paymentHandler.GetPaymentByID)
		api.GET("/by-order/:orderID", middleware.RequirePermission(domain.PermissionPaymentsRead), paymentHandler.GetPaymentByOrderID)
		api.GET("/debt/:orderID", middleware.RequirePermission(domain.PermissionPaymentsRead), paymentHandler.GetPayableDebtByOrderID)
		api.PUT("/config", middleware.RequirePermission(domain.PermissionPaymentsManage), paymentHandler.UpdatePSPConfig)
		api.GET("/config", middleware.RequirePermission(domain.PermissionPaymentsManage), paymentHandler.GetPSPConfig)
	}

	return r
}
