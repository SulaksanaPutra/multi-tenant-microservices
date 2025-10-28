package main

import (
	"net/http"

	"tenant-service/internal/handler"
	"tenant-service/internal/middleware"
	"tenant-service/internal/service"
	"tenant-service/internal/txcontext"

	"github.com/gin-gonic/gin"
)

func newRouter(txManager txcontext.TxManager, workspaceService *service.WorkspaceService, internalToken string) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	workspaceHandler := handler.NewWorkspaceHandler(txManager, workspaceService)

	// Public registration endpoint
	r.POST("/api/register", workspaceHandler.RegisterWorkspace)

	// Protected Internal Control Plane routing endpoints (Zero-Trust)
	internal := r.Group("/internal/tenants")
	internal.Use(middleware.InternalAuthMiddleware(internalToken))
	{
		internal.GET("/:tenant_id/infrastructure/:service_name", workspaceHandler.GetServiceInfrastructure)
	}

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	return r
}
