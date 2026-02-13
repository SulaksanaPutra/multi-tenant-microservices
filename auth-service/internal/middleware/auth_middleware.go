package middleware

import (
	"net/http"

	"auth-service/internal/httputil"

	"github.com/gin-gonic/gin"
)

// InternalAuthMiddleware enforces that calls to internal endpoints
// carry a valid X-Internal-Service-Token header.
func InternalAuthMiddleware(expectedToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if expectedToken == "" {
			expectedToken = "default_internal_service_token"
		}

		token := c.GetHeader("X-Internal-Service-Token")
		if token == "" || token != expectedToken {
			httputil.WriteError(c, http.StatusForbidden, "Unauthorized inter-service access: invalid or missing X-Internal-Service-Token")
			c.Abort()
			return
		}

		c.Next()
	}
}
