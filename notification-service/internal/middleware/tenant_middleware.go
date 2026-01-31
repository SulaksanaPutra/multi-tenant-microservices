package middleware

import (
	"net/http"

	"notification-service/internal/httputil"

	"github.com/gin-gonic/gin"
)

// RequireTenantHeader validates the presence of X-Tenant-ID header (or tenant_id query param).
func RequireTenantHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.GetHeader("X-Tenant-ID")
		if tenantID == "" {
			tenantID = c.Query("tenant_id")
		}
		if tenantID == "" {
			httputil.WriteError(c, http.StatusUnauthorized, "X-Tenant-ID header is required")
			c.Abort()
			return
		}
		c.Set("tenantID", tenantID)
		c.Next()
	}
}
