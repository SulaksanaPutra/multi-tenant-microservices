package main

import (
	"errors"
	"net/http"
	"os"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
	"order-service/internal/domain"
	"order-service/internal/handler"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/repository"
	"order-service/internal/service"

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

	orderHandler := handler.NewOrderHandler(func(cfg tenantdb.Config) handler.OrderService {
		return service.NewOrderService(repository.NewOrderRepository(cfg))
	})

	tenantHandlerHook := func(c *gin.Context, tenantID string) error {
		tenantCfg, err := tenantDBResolver.GetTenantDB(c.Request.Context(), tenantID)
		if err != nil {
			if errors.Is(err, domain.ErrTenantMigrating) {
				httputil.WriteError(c, http.StatusLocked, "tenant infrastructure is locked for migration")
				return err
			}
			httputil.WriteError(c, http.StatusInternalServerError, "failed to resolve tenant database: "+err.Error())
			return err
		}
		c.Set("tenantConfig", tenantCfg)
		ctx := tenantdb.WithConfig(c.Request.Context(), tenantCfg)
		c.Request = c.Request.WithContext(ctx)
		return nil
	}

	api := r.Group("/api")
	api.Use(middleware.RequireJWT(
		publicKeyPEM,
		middleware.WithVersionCache(versionCache),
		middleware.WithTenantHandler(tenantHandlerHook),
	))

	api.GET("/orders", middleware.RequirePermission("orders:read"), orderHandler.ListOrders)
	api.POST("/orders", middleware.RequirePermission("orders:write"), orderHandler.CreateOrder)

	return r
}
