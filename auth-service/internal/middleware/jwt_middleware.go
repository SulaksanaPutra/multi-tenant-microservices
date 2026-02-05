package middleware

import (
	"net/http"
	"strings"

	"auth-service/internal/crypto"
	"auth-service/internal/httputil"

	"github.com/gin-gonic/gin"
)

const (
	// ContextKeyUserID is the gin.Context key for the authenticated user's ID.
	ContextKeyUserID = "userID"
	// ContextKeyTenantID is the gin.Context key for the authenticated tenant's ID.
	ContextKeyTenantID = "tenantID"
	// ContextKeyEmail is the gin.Context key for the authenticated user's email.
	ContextKeyEmail = "email"
	// ContextKeyJTI is the gin.Context key for the JWT's unique token ID.
	ContextKeyJTI = "jti"
)

// RequireJWT validates the RS256 Bearer token on incoming requests and injects
// the verified JWT claims into the gin.Context.
//
// This middleware is used by auth-service itself to protect the /auth/logout endpoint.
// Consumer services (order-service, notification-service) carry their own copy of
// this middleware pattern, verified against the distributed public key.
func RequireJWT(jwtManager *crypto.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			httputil.WriteError(c, http.StatusUnauthorized, "Authorization header is required")
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			httputil.WriteError(c, http.StatusUnauthorized, "Authorization header must be 'Bearer <token>'")
			c.Abort()
			return
		}

		claims, err := jwtManager.VerifyAccessToken(parts[1])
		if err != nil {
			httputil.WriteError(c, http.StatusUnauthorized, "invalid or expired token")
			c.Abort()
			return
		}

		c.Set(ContextKeyUserID, claims.UserID)
		c.Set(ContextKeyTenantID, claims.TenantID)
		c.Set(ContextKeyEmail, claims.Email)
		c.Set(ContextKeyJTI, claims.JTI)

		c.Next()
	}
}
