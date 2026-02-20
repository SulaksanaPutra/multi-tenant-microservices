package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"auth-service/internal/httputil"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestInternalAuthMiddleware(t *testing.T) {
	expectedToken := "secret_internal_token"
	handler := InternalAuthMiddleware(expectedToken)

	t.Run("missing header -> 403 Forbidden", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		r.Use(handler)
		r.GET("/internal/test", func(c *gin.Context) {
			httputil.WriteSuccess[any](c, http.StatusOK, "ok", nil)
		})

		req := httptest.NewRequest(http.MethodGet, "/internal/test", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected status %d, got %d", http.StatusForbidden, w.Code)
		}

		var resp httputil.ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Error == "" {
			t.Errorf("expected error message in response body")
		}
	})

	t.Run("invalid token header -> 403 Forbidden", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		r.Use(handler)
		r.GET("/internal/test", func(c *gin.Context) {
			httputil.WriteSuccess[any](c, http.StatusOK, "ok", nil)
		})

		req := httptest.NewRequest(http.MethodGet, "/internal/test", nil)
		req.Header.Set("X-Internal-Service-Token", "wrong_token")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("expected status %d, got %d", http.StatusForbidden, w.Code)
		}
	})

	t.Run("valid token header -> 200 OK", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		r.Use(handler)
		r.GET("/internal/test", func(c *gin.Context) {
			httputil.WriteSuccess[any](c, http.StatusOK, "ok", nil)
		})

		req := httptest.NewRequest(http.MethodGet, "/internal/test", nil)
		req.Header.Set("X-Internal-Service-Token", expectedToken)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
		}
	})

	t.Run("empty expectedToken fallback to default", func(t *testing.T) {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)

		defaultHandler := InternalAuthMiddleware("")
		r.Use(defaultHandler)
		r.GET("/internal/test", func(c *gin.Context) {
			httputil.WriteSuccess[any](c, http.StatusOK, "ok", nil)
		})

		req := httptest.NewRequest(http.MethodGet, "/internal/test", nil)
		req.Header.Set("X-Internal-Service-Token", "default_internal_service_token")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected default token to work, got status %d", w.Code)
		}
	})
}
