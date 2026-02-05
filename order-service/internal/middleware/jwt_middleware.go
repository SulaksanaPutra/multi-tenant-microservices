package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"order-service/internal/httputil"
	"order-service/internal/infrastructure/tenantdb"

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

// Resolver is the consumer-side interface expected by the JWT middleware.
type Resolver interface {
	GetTenantDB(ctx context.Context, tenantID string) (tenantdb.Config, error)
}

// jwtClaims mirrors the JWT payload structure issued by auth-service.
type jwtClaims struct {
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
	jwt.RegisteredClaims
}

// RequireJWT validates an RS256 Bearer token, extracts the tenant_id claim,
// resolves the tenant database configuration, and injects both into gin.Context.
//
// The RSA public key is loaded once at startup from AUTH_JWT_PUBLIC_KEY_PEM and
// passed in here. No runtime round-trip to auth-service is required for verification.
func RequireJWT(publicKeyPEM string, resolver Resolver) gin.HandlerFunc {
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(publicKeyPEM))
	if err != nil {
		// Panic at startup — a misconfigured public key must be caught early.
		panic(fmt.Sprintf("order-service: failed to parse AUTH_JWT_PUBLIC_KEY_PEM: %v", err))
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

		tenantID := claims.TenantID
		if tenantID == "" {
			httputil.WriteError(c, http.StatusUnauthorized, "token missing tenant_id claim")
			c.Abort()
			return
		}

		tenantCfg, err := resolver.GetTenantDB(c.Request.Context(), tenantID)
		if err != nil {
			httputil.WriteError(c, http.StatusInternalServerError, fmt.Sprintf("failed to resolve tenant database: %v", err))
			c.Abort()
			return
		}

		c.Set(ContextKeyUserID, claims.Subject)
		c.Set(ContextKeyTenantID, tenantID)
		c.Set(ContextKeyEmail, claims.Email)
		c.Set("tenantConfig", tenantCfg)

		ctx := tenantdb.WithConfig(c.Request.Context(), tenantCfg)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
