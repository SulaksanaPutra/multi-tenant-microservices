package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"notification-service/internal/httputil"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	// ContextKeyUserID is the gin.Context key for the authenticated user's ID.
	ContextKeyUserID = "userID"
	// ContextKeyTenantID is the gin.Context key for the authenticated tenant's ID.
	ContextKeyTenantID = "tenantID"
	// ContextKeyEmail is the gin.Context key for the authenticated user's email.
	ContextKeyEmail = "email"
)

// jwtClaims mirrors the JWT payload structure issued by auth-service.
type jwtClaims struct {
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
	jwt.RegisteredClaims
}

// RequireJWT validates an RS256 Bearer token on incoming requests and injects
// the verified JWT claims (userID, tenantID, email) into the gin.Context.
//
// The RSA public key is loaded once at startup from AUTH_JWT_PUBLIC_KEY_PEM.
// No runtime round-trip to auth-service is required for verification.
func RequireJWT(publicKeyPEM string) gin.HandlerFunc {
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(publicKeyPEM))
	if err != nil {
		// Panic at startup — a misconfigured public key must be caught early.
		panic(fmt.Sprintf("notification-service: failed to parse AUTH_JWT_PUBLIC_KEY_PEM: %v", err))
	}

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

		token, err := jwt.ParseWithClaims(parts[1], &jwtClaims{}, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return pubKey, nil
		})
		if err != nil || !token.Valid {
			httputil.WriteError(c, http.StatusUnauthorized, "invalid or expired token")
			c.Abort()
			return
		}

		claims, ok := token.Claims.(*jwtClaims)
		if !ok {
			httputil.WriteError(c, http.StatusUnauthorized, "malformed token claims")
			c.Abort()
			return
		}

		if claims.TenantID == "" {
			httputil.WriteError(c, http.StatusUnauthorized, "token missing tenant_id claim")
			c.Abort()
			return
		}

		c.Set(ContextKeyUserID, claims.Subject)
		c.Set(ContextKeyTenantID, claims.TenantID)
		c.Set(ContextKeyEmail, claims.Email)

		c.Next()
	}
}
