package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"payment-service/internal/httputil"
)

const (
	ContextKeyUserID      = "userID"
	ContextKeyTenantID    = "tenantID"
	ContextKeyEmail       = "email"
	ContextKeyPermissions = "permissions"
	ContextKeyPermVersion = "permVersion"
)

type jwtClaims struct {
	TenantID    string   `json:"tenant_id"`
	Email       string   `json:"email"`
	Permissions []string `json:"permissions,omitempty"`
	PermVersion int64    `json:"perm_version,omitempty"`
	jwt.RegisteredClaims
}

func RequireJWT(publicKeyPEM string) gin.HandlerFunc {
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(publicKeyPEM))
	if err != nil {
		panic(fmt.Sprintf("payment-service: failed to parse AUTH_JWT_PUBLIC_KEY_PEM: %v", err))
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

		c.Set(ContextKeyUserID, claims.Subject)
		c.Set(ContextKeyTenantID, tenantID)
		c.Set(ContextKeyEmail, claims.Email)
		c.Set(ContextKeyPermissions, claims.Permissions)
		c.Set(ContextKeyPermVersion, claims.PermVersion)

		c.Next()
	}
}

func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := c.Get(ContextKeyPermissions)
		if !ok {
			httputil.WriteError(c, http.StatusForbidden, "access denied: missing permissions claim")
			c.Abort()
			return
		}

		perms, ok := raw.([]string)
		if !ok {
			httputil.WriteError(c, http.StatusForbidden, "access denied: invalid permissions claim type")
			c.Abort()
			return
		}

		for _, p := range perms {
			if p == permission {
				c.Next()
				return
			}
		}

		httputil.WriteError(c, http.StatusForbidden, fmt.Sprintf("access denied: missing permission '%s'", permission))
		c.Abort()
	}
}
