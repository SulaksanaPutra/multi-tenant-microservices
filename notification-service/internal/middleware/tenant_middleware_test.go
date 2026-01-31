package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequireTenantHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing X-Tenant-ID header and query returns 401 Unauthorized", func(t *testing.T) {
		r := gin.New()
		r.Use(RequireTenantHeader())
		r.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", w.Code)
		}
	})

	t.Run("valid X-Tenant-ID header passes through and injects tenantID", func(t *testing.T) {
		r := gin.New()
		var capturedTenantID string
		r.Use(RequireTenantHeader())
		r.GET("/test", func(c *gin.Context) {
			capturedTenantID = c.GetString("tenantID")
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Tenant-ID", "tenant-123")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
		if capturedTenantID != "tenant-123" {
			t.Errorf("expected tenantID 'tenant-123', got '%s'", capturedTenantID)
		}
	})

	t.Run("valid tenant_id query parameter passes through and injects tenantID", func(t *testing.T) {
		r := gin.New()
		var capturedTenantID string
		r.Use(RequireTenantHeader())
		r.GET("/test", func(c *gin.Context) {
			capturedTenantID = c.GetString("tenantID")
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test?tenant_id=tenant-456", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
		if capturedTenantID != "tenant-456" {
			t.Errorf("expected tenantID 'tenant-456', got '%s'", capturedTenantID)
		}
	})
}
