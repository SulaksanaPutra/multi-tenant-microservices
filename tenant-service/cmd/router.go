package main

import (
	"net/http"
	"os"

	"tenant-service/internal/handler"
	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
	"tenant-service/internal/service"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"

	"github.com/gin-gonic/gin"
)

func newRouter(txManager *txcontext.SQLTxManager, workspaceService *service.WorkspaceService, tenantInfrastructureService *service.TenantInfrastructureService, internalToken string) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	workspaceHandler := handler.NewWorkspaceHandler(txManager, workspaceService)
	internalTenantHandler := handler.NewInternalTenantHandler(tenantInfrastructureService)
	tenantHandler := handler.NewTenantHandler(workspaceService)

	// Public registration endpoint
	r.POST("/api/tenants/register", workspaceHandler.RegisterWorkspace)

	publicKeyPEM := os.Getenv("AUTH_JWT_PUBLIC_KEY_PEM")
	if publicKeyPEM != "" {
		authServiceURL := os.Getenv("AUTH_SERVICE_URL")
		versionCache := middleware.NewVersionCache(authServiceURL, internalToken)

		api := r.Group("/api/tenants")
		api.Use(middleware.RequireJWT(publicKeyPEM, middleware.WithVersionCache(versionCache)))
		{
			api.GET("/me", middleware.RequirePermission("tenants:read"), tenantHandler.GetTenantMe)
			api.PUT("/me", middleware.RequirePermission("tenants:write"), tenantHandler.UpdateTenantMe)
			api.PUT("/me/plan", middleware.RequirePermission("tenants:write"), tenantHandler.ChangeTenantPlanMe)
		}
	}

	// Protected Internal Control Plane routing endpoints (Zero-Trust)
	internal := r.Group("/internal/tenants")
	internal.Use(middleware.InternalAuthMiddleware(internalToken))
	{
		internal.GET("/:tenant_id/infrastructure/:service_name", internalTenantHandler.GetServiceInfrastructure)
	}

	// Health check
	r.GET("/health", func(c *gin.Context) {
		httputil.WriteSuccess[any](c, http.StatusOK, "OK", nil)
	})

	return r
}
