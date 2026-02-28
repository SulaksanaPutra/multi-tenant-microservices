package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

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

func signTestToken(t *testing.T, privateKey *rsa.PrivateKey, tenantID, userID string, perms []string) string {
	t.Helper()
	claims := jwtClaims{
		TenantID:    tenantID,
		Email:       "user@example.com",
		Permissions: perms,
		PermVersion: 1,
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

func TestRequireJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	privateKey, pubKeyPEM := generateTestKeyPair(t)

	t.Run("missing Authorization header returns 401", func(t *testing.T) {
		r := gin.New()
		r.Use(RequireJWT(pubKeyPEM))
		r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("valid JWT injects context claims", func(t *testing.T) {
		r := gin.New()
		r.Use(RequireJWT(pubKeyPEM))

		var capturedTenantID, capturedUserID string
		r.GET("/test", func(c *gin.Context) {
			capturedTenantID = c.GetString(ContextKeyTenantID)
			capturedUserID = c.GetString(ContextKeyUserID)
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		token := signTestToken(t, privateKey, "ten_789", "usr_123", []string{"users:read"})
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if capturedTenantID != "ten_789" || capturedUserID != "usr_123" {
			t.Errorf("unexpected context values: tenant=%s, user=%s", capturedTenantID, capturedUserID)
		}
	})
}

func TestRequirePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing permissions claim returns 403", func(t *testing.T) {
		r := gin.New()
		r.Use(RequirePermission("users:read"))
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
			c.Set(ContextKeyPermissions, []string{"users:read", "users:update"})
			c.Next()
		})
		r.Use(RequirePermission("users:read"))
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
