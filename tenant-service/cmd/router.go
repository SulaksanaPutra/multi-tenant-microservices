package main

import (
	"net/http"

	"tenant-service/internal/handler"
	"tenant-service/internal/service"
	"tenant-service/internal/txctx"

	"github.com/gin-gonic/gin"
)

func newRouter(txManager txctx.TxManager, workspaceService service.WorkspaceService) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	workspaceHandler := handler.NewWorkspaceHandler(txManager, workspaceService)

	// Public registration endpoint
	r.POST("/api/register", workspaceHandler.RegisterWorkspace)

	// Internal Control Plane endpoints
	internal := r.Group("/internal/tenants")
	{
		internal.PATCH("/:tenant_id/infrastructure", workspaceHandler.UpdateInfrastructure)
		internal.GET("/:tenant_id/infrastructure/:service_name", workspaceHandler.GetServiceInfrastructure)
	}

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	return r
}
