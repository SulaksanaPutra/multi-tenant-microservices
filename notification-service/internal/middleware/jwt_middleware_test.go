package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
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
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	return privateKey, pubPEM
}

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

	t.Run("malformed token returns 401", func(t *testing.T) {
		r := gin.New()
		r.Use(RequireJWT(pubKeyPEM))
		r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer not-a-valid-jwt")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("valid JWT injects tenantID and userID into context", func(t *testing.T) {
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
		token := signTestToken(t, privateKey, "ten_notif123", "usr_notif456")
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if capturedTenantID != "ten_notif123" {
			t.Errorf("expected tenantID 'ten_notif123', got '%s'", capturedTenantID)
		}
		if capturedUserID != "usr_notif456" {
			t.Errorf("expected userID 'usr_notif456', got '%s'", capturedUserID)
		}
	})
}
