package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"order-service/internal/domain"
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
	// ContextKeyPermissions is the gin.Context key for the user's permissions.
	ContextKeyPermissions = "permissions"
	// ContextKeyPermVersion is the gin.Context key for the token's permission version.
	ContextKeyPermVersion = "permVersion"
)

// Resolver is the consumer-side interface expected by the JWT middleware.
type Resolver interface {
	GetTenantDB(ctx context.Context, tenantID string) (tenantdb.Config, error)
}

// VersionCache provides an in-memory 60-second TTL cache for user permission versions
// to enforce instant token revocation across downstream services.
type VersionCache struct {
	mu                   sync.RWMutex
	cache                map[string]cacheEntry
	authServiceURL       string
	internalServiceToken string
	httpClient           *http.Client
	ttl                  time.Duration
}

type cacheEntry struct {
	version   int64
	expiresAt time.Time
}

func NewVersionCache(authServiceURL, internalServiceToken string) *VersionCache {
	if authServiceURL == "" {
		authServiceURL = "http://auth-service:8085"
	}
	if internalServiceToken == "" {
		internalServiceToken = "default_internal_service_token"
	}
	return &VersionCache{
		cache:                make(map[string]cacheEntry),
		authServiceURL:       authServiceURL,
		internalServiceToken: internalServiceToken,
		httpClient:           &http.Client{Timeout: 3 * time.Second},
		ttl:                  60 * time.Second,
	}
}

func (vc *VersionCache) VerifyVersion(ctx context.Context, userID, tenantID string, tokenPermVersion int64) bool {
	if tokenPermVersion == 0 || userID == "" || tenantID == "" {
		return true
	}

	key := userID + ":" + tenantID

	vc.mu.RLock()
	entry, exists := vc.cache[key]
	vc.mu.RUnlock()

	if exists && time.Now().Before(entry.expiresAt) {
		return entry.version <= tokenPermVersion
	}

	url := fmt.Sprintf("%s/internal/auth/users/%s/perm-version?tenant_id=%s", vc.authServiceURL, userID, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return true
	}
	req.Header.Set("X-Internal-Service-Token", vc.internalServiceToken)

	resp, err := vc.httpClient.Do(req)
	if err != nil {
		return true
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return true
	}

	var versionResp struct {
		Data struct {
			PermVersion int64 `json:"perm_version"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&versionResp); err != nil {
		return true
	}

	fetchedVersion := versionResp.Data.PermVersion

	vc.mu.Lock()
	vc.cache[key] = cacheEntry{
		version:   fetchedVersion,
		expiresAt: time.Now().Add(vc.ttl),
	}
	vc.mu.Unlock()

	return fetchedVersion <= tokenPermVersion
}

// jwtClaims mirrors the JWT payload structure issued by auth-service.
type jwtClaims struct {
	TenantID    string   `json:"tenant_id"`
	Email       string   `json:"email"`
	Permissions []string `json:"permissions,omitempty"`
	PermVersion int64    `json:"perm_version,omitempty"`
	jwt.RegisteredClaims
}

// RequireJWT validates an RS256 Bearer token, checks perm_version against VersionCache,
// extracts the tenant_id claim, resolves tenant DB configuration, and injects claims into gin.Context.
func RequireJWT(publicKeyPEM string, resolver Resolver, versionCache ...*VersionCache) gin.HandlerFunc {
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(publicKeyPEM))
	if err != nil {
		panic(fmt.Sprintf("order-service: failed to parse AUTH_JWT_PUBLIC_KEY_PEM: %v", err))
	}

	var vCache *VersionCache
	if len(versionCache) > 0 && versionCache[0] != nil {
		vCache = versionCache[0]
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

		if vCache != nil && claims.PermVersion > 0 {
			if valid := vCache.VerifyVersion(c.Request.Context(), claims.Subject, tenantID, claims.PermVersion); !valid {
				httputil.WriteError(c, http.StatusUnauthorized, "token superseded: permissions updated")
				c.Abort()
				return
			}
		}

		tenantCfg, err := resolver.GetTenantDB(c.Request.Context(), tenantID)
		if err != nil {
			if errors.Is(err, domain.ErrTenantMigrating) {
				httputil.WriteError(c, http.StatusLocked, "tenant infrastructure is locked for migration")
				c.Abort()
				return
			}
			httputil.WriteError(c, http.StatusInternalServerError, fmt.Sprintf("failed to resolve tenant database: %v", err))
			c.Abort()
			return
		}

		c.Set(ContextKeyUserID, claims.Subject)
		c.Set(ContextKeyTenantID, tenantID)
		c.Set(ContextKeyEmail, claims.Email)
		c.Set(ContextKeyPermissions, claims.Permissions)
		c.Set(ContextKeyPermVersion, claims.PermVersion)
		c.Set("tenantConfig", tenantCfg)

		ctx := tenantdb.WithConfig(c.Request.Context(), tenantCfg)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// RequirePermission checks whether the authenticated user has the specified permission string in claims.
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
