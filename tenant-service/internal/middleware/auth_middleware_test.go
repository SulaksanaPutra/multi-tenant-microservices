package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestInternalAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("valid token allows request to proceed", func(t *testing.T) {
		r := gin.New()
		r.Use(InternalAuthMiddleware("valid-secret-token"))

		handlerExecuted := false
		r.GET("/internal/test", func(c *gin.Context) {
			handlerExecuted = true
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/internal/test", nil)
		req.Header.Set("X-Internal-Service-Token", "valid-secret-token")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
		if !handlerExecuted {
			t.Error("expected downstream handler to be executed")
		}
	})

	t.Run("missing X-Internal-Service-Token header returns 403 Forbidden", func(t *testing.T) {
		r := gin.New()
		r.Use(InternalAuthMiddleware("valid-secret-token"))

		handlerExecuted := false
		r.GET("/internal/test", func(c *gin.Context) {
			handlerExecuted = true
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/internal/test", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
		}
		if handlerExecuted {
			t.Error("expected downstream handler NOT to be executed")
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse JSON response: %v", err)
		}
		if resp["error"] == nil {
			t.Error("expected error message in JSON response")
		}
	})

	t.Run("invalid X-Internal-Service-Token header returns 403 Forbidden", func(t *testing.T) {
		r := gin.New()
		r.Use(InternalAuthMiddleware("valid-secret-token"))

		handlerExecuted := false
		r.GET("/internal/test", func(c *gin.Context) {
			handlerExecuted = true
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/internal/test", nil)
		req.Header.Set("X-Internal-Service-Token", "invalid-token")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
		}
		if handlerExecuted {
			t.Error("expected downstream handler NOT to be executed")
		}
	})

	t.Run("empty expectedToken defaults to default_internal_service_token and permits match", func(t *testing.T) {
		r := gin.New()
		r.Use(InternalAuthMiddleware(""))

		handlerExecuted := false
		r.GET("/internal/test", func(c *gin.Context) {
			handlerExecuted = true
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/internal/test", nil)
		req.Header.Set("X-Internal-Service-Token", "default_internal_service_token")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
		if !handlerExecuted {
			t.Error("expected downstream handler to be executed when default token matches")
		}
	})

	t.Run("empty expectedToken rejects token non-matching default", func(t *testing.T) {
		r := gin.New()
		r.Use(InternalAuthMiddleware(""))

		handlerExecuted := false
		r.GET("/internal/test", func(c *gin.Context) {
			handlerExecuted = true
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/internal/test", nil)
		req.Header.Set("X-Internal-Service-Token", "some-other-token")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
		}
		if handlerExecuted {
			t.Error("expected downstream handler NOT to be executed")
		}
	})
}
