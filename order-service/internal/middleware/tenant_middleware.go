package middleware

import (
	"context"
	"fmt"
	"net/http"

	"order-service/internal/httputil"
	"order-service/internal/infrastructure/tenantdb"

	"github.com/gin-gonic/gin"
)

// Resolver is the consumer-side interface expected by tenant middleware.
type Resolver interface {
	GetTenantDB(ctx context.Context, tenantID string) (tenantdb.Config, error)
}

// RequireTenantHeader validates the X-Tenant-ID header and resolves the tenant database configuration.
// It injects tenantID and tenantConfig into gin.Context.
func RequireTenantHeader(resolver Resolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.GetHeader("X-Tenant-ID")
		if tenantID == "" {
			httputil.WriteError(c, http.StatusBadRequest, "X-Tenant-ID header is required")
			c.Abort()
			return
		}

		tenantCfg, err := resolver.GetTenantDB(c.Request.Context(), tenantID)
		if err != nil {
			httputil.WriteError(c, http.StatusInternalServerError, fmt.Sprintf("failed to resolve tenant database: %v", err))
			c.Abort()
			return
		}

		c.Set("tenantID", tenantID)
		c.Set("tenantConfig", tenantCfg)

		ctx := tenantdb.WithConfig(c.Request.Context(), tenantCfg)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
