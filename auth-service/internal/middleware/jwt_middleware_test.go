package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"auth-service/internal/crypto"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func generateTestPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(privateKey)
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: der,
	}))
}

func TestRequireJWT(t *testing.T) {
	pemStr := generateTestPrivateKeyPEM(t)
	jwtMgr, err := crypto.NewJWTManager(pemStr)
	if err != nil {
		t.Fatalf("failed to create JWTManager: %v", err)
	}

	t.Run("missing Authorization header -> 401", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		r.Use(RequireJWT(jwtMgr))
		r.GET("/protected", func(c *gin.Context) {})

		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", w.Code)
		}
	})

	t.Run("malformed Authorization header -> 401", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		r.Use(RequireJWT(jwtMgr))
		r.GET("/protected", func(c *gin.Context) {})

		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", w.Code)
		}
	})

	t.Run("Signature Mismatch (signed by different private key) -> 401", func(t *testing.T) {
		differentPemStr := generateTestPrivateKeyPEM(t)
		differentJwtMgr, err := crypto.NewJWTManager(differentPemStr)
		if err != nil {
			t.Fatalf("failed to create different JWTManager: %v", err)
		}

		forgedToken, err := differentJwtMgr.SignAccessToken("usr_forged", "tnt_1", "forged@example.com", "jti_forged")
		if err != nil {
			t.Fatalf("failed to sign forged token: %v", err)
		}

		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		r.Use(RequireJWT(jwtMgr))
		r.GET("/protected", func(c *gin.Context) {})

		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+forgedToken)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401 on signature mismatch, got %d", w.Code)
		}
	})

	t.Run("Algorithm Verification (reject HS256 / none) -> 401", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":       "usr_123",
			"tenant_id": "tnt_456",
			"email":     "test@example.com",
		})
		hs256TokenStr, err := token.SignedString([]byte("secret"))
		if err != nil {
			t.Fatalf("failed to create HS256 token: %v", err)
		}

		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		r.Use(RequireJWT(jwtMgr))
		r.GET("/protected", func(c *gin.Context) {})

		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+hs256TokenStr)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401 when using HS256 algorithm, got %d", w.Code)
		}
	})

	t.Run("valid RS256 token -> 200 OK with context claims set", func(t *testing.T) {
		userID := "usr_valid_123"
		tenantID := "tnt_valid_456"
		email := "valid@example.com"
		jti := "jti_valid_789"

		tokenStr, err := jwtMgr.SignAccessToken(userID, tenantID, email, jti)
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}

		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		var capturedUserID, capturedTenantID, capturedEmail, capturedJTI string

		r.Use(RequireJWT(jwtMgr))
		r.GET("/protected", func(c *gin.Context) {
			val, _ := c.Get(ContextKeyUserID)
			capturedUserID, _ = val.(string)

			val, _ = c.Get(ContextKeyTenantID)
			capturedTenantID, _ = val.(string)

			val, _ = c.Get(ContextKeyEmail)
			capturedEmail, _ = val.(string)

			val, _ = c.Get(ContextKeyJTI)
			capturedJTI, _ = val.(string)

			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+tokenStr)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}

		if capturedUserID != userID || capturedTenantID != tenantID || capturedEmail != email || capturedJTI != jti {
			t.Errorf("unexpected context values: userID=%s, tenantID=%s, email=%s, jti=%s",
				capturedUserID, capturedTenantID, capturedEmail, capturedJTI)
		}
	})
}
