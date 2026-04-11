package middleware

import (
	"net/http"

	"auth-service/internal/httputil"

	"github.com/gin-gonic/gin"
)

// RequirePermission enforces that the authenticated JWT carries the required
// permission string. It must be chained after RequireJWT so that the validated
// claims, including the bundled permission list, are available on the context.
//
// This makes auth-service the authorization enforcement point for its own
// tenant-admin RBAC endpoints, closing the gap where only authentication (a
// valid JWT) was previously required.
func RequirePermission(required string) gin.HandlerFunc {
	return func(c *gin.Context) {
		perms, _ := c.Get(ContextKeyPermissions)
		list, ok := perms.([]string)
		if !ok {
			httputil.WriteError(c, http.StatusForbidden, "missing permission context; expected RequireJWT to run first")
			c.Abort()
			return
		}

		for _, perm := range list {
			if perm == required {
				c.Next()
				return
			}
		}

		httputil.WriteError(c, http.StatusForbidden, "insufficient permissions: '"+required+"' is required")
		c.Abort()
	}
}
