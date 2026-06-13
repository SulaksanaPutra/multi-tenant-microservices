package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"order-service/internal/infrastructure/tenantdb"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// generateTestKeyPair produces a fresh RSA-2048 key pair for use in tests only.
func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test RSA key: %v", err)
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal test RSA public key: %v", err)
	}

	pubPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	}))

	return privateKey, pubPEM
}

// signTestToken creates a signed RS256 JWT for the given tenant/user IDs.
func signTestToken(t *testing.T, privateKey *rsa.PrivateKey, tenantID, userID string) string {
	t.Helper()
	claims := jwtClaims{
		TenantID: tenantID,
		Email:    "test@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			ID:        "test-jti",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign test JWT: %v", err)
	}
	return signed
}

type mockResolver struct {
	GetTenantDBFn func(ctx context.Context, tenantID string) (tenantdb.Config, error)
}

func (m *mockResolver) GetTenantDB(ctx context.Context, tenantID string) (tenantdb.Config, error) {
	if m.GetTenantDBFn != nil {
		return m.GetTenantDBFn(ctx, tenantID)
	}
	return tenantdb.Config{}, nil
}

func TestRequireJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	privateKey, pubKeyPEM := generateTestKeyPair(t)

	t.Run("missing Authorization header returns 401", func(t *testing.T) {
		r := gin.New()
		r.Use(RequireJWT(pubKeyPEM, &mockResolver{}))
		r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("malformed Bearer token returns 401", func(t *testing.T) {
		r := gin.New()
		r.Use(RequireJWT(pubKeyPEM, &mockResolver{}))
		r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer not-a-valid-jwt")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("resolver error returns 500", func(t *testing.T) {
		resolver := &mockResolver{
			GetTenantDBFn: func(_ context.Context, _ string) (tenantdb.Config, error) {
				return tenantdb.Config{}, errors.New("db resolution failed")
			},
		}
		r := gin.New()
		r.Use(RequireJWT(pubKeyPEM, resolver))
		r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		token := signTestToken(t, privateKey, "ten_abc123", "usr_test")
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})

	t.Run("valid JWT injects tenantID, userID, and tenantConfig into context", func(t *testing.T) {
		expectedCfg := tenantdb.Config{TenantID: "ten_abc123", SchemaName: "tenant_abc123"}
		resolver := &mockResolver{
			GetTenantDBFn: func(_ context.Context, _ string) (tenantdb.Config, error) {
				return expectedCfg, nil
			},
		}

		r := gin.New()
		r.Use(RequireJWT(pubKeyPEM, resolver))

		var capturedTenantID, capturedUserID string
		var capturedConfig tenantdb.Config

		r.GET("/test", func(c *gin.Context) {
			capturedTenantID = c.GetString(ContextKeyTenantID)
			capturedUserID = c.GetString(ContextKeyUserID)
			if cfgVal, exists := c.Get("tenantConfig"); exists {
				capturedConfig = cfgVal.(tenantdb.Config)
			}
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		token := signTestToken(t, privateKey, "ten_abc123", "usr_test123")
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if capturedTenantID != "ten_abc123" {
			t.Errorf("expected tenantID 'ten_abc123', got '%s'", capturedTenantID)
		}
		if capturedUserID != "usr_test123" {
			t.Errorf("expected userID 'usr_test123', got '%s'", capturedUserID)
		}
		if capturedConfig.SchemaName != expectedCfg.SchemaName {
			t.Errorf("expected SchemaName '%s', got '%s'", expectedCfg.SchemaName, capturedConfig.SchemaName)
		}
	})
}

func TestRequirePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing permissions claim returns 403", func(t *testing.T) {
		r := gin.New()
		r.Use(RequirePermission("orders:write"))
		r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", w.Code)
		}
	})

	t.Run("permission missing in claims returns 403", func(t *testing.T) {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(ContextKeyPermissions, []string{"orders:read"})
			c.Next()
		})
		r.Use(RequirePermission("orders:write"))
		r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", w.Code)
		}
	})

	t.Run("matching permission in claims returns 200", func(t *testing.T) {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(ContextKeyPermissions, []string{"orders:read", "orders:write"})
			c.Next()
		})
		r.Use(RequirePermission("orders:write"))
		r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestVersionCache(t *testing.T) {
	vc := NewVersionCache("http://mock-auth-service", "test-token")

	t.Run("zero version token always returns true", func(t *testing.T) {
		valid := vc.VerifyVersion(context.Background(), "user1", "tenant1", 0)
		if !valid {
			t.Errorf("expected true for tokenPermVersion 0")
		}
	})

	t.Run("cached higher version rejects lower version token", func(t *testing.T) {
		vc.mu.Lock()
		vc.cache["user1:tenant1"] = cacheEntry{
			version:   2,
			expiresAt: time.Now().Add(10 * time.Minute),
		}
		vc.mu.Unlock()

		valid := vc.VerifyVersion(context.Background(), "user1", "tenant1", 1)
		if valid {
			t.Errorf("expected false for tokenPermVersion 1 when cached version is 2")
		}

		validEqual := vc.VerifyVersion(context.Background(), "user1", "tenant1", 2)
		if !validEqual {
			t.Errorf("expected true for tokenPermVersion 2 when cached version is 2")
		}
	})
}
