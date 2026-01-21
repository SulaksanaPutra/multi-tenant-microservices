package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"order-service/internal/infrastructure/tenantdb"

	"github.com/gin-gonic/gin"
)

type mockResolver struct {
	GetTenantDBFn func(ctx context.Context, tenantID string) (tenantdb.Config, error)
}

func (m *mockResolver) GetTenantDB(ctx context.Context, tenantID string) (tenantdb.Config, error) {
	if m.GetTenantDBFn != nil {
		return m.GetTenantDBFn(ctx, tenantID)
	}
	return tenantdb.Config{}, nil
}

func TestRequireTenantHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing X-Tenant-ID header returns 400", func(t *testing.T) {
		r := gin.New()
		resolver := &mockResolver{}
		r.Use(RequireTenantHeader(resolver))
		r.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("resolver error returns 500", func(t *testing.T) {
		r := gin.New()
		resolver := &mockResolver{
			GetTenantDBFn: func(ctx context.Context, tenantID string) (tenantdb.Config, error) {
				return tenantdb.Config{}, errors.New("db resolution failed")
			},
		}
		r.Use(RequireTenantHeader(resolver))
		r.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Tenant-ID", "tenant-123")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected status 500, got %d", w.Code)
		}
	})

	t.Run("successful tenant resolution injects context", func(t *testing.T) {
		r := gin.New()
		expectedCfg := tenantdb.Config{TenantID: "tenant-456", SchemaName: "tenant_456"}
		resolver := &mockResolver{
			GetTenantDBFn: func(ctx context.Context, tenantID string) (tenantdb.Config, error) {
				return expectedCfg, nil
			},
		}

		var capturedTenantID string
		var capturedConfig tenantdb.Config

		r.Use(RequireTenantHeader(resolver))
		r.GET("/test", func(c *gin.Context) {
			capturedTenantID = c.GetString("tenantID")
			if cfgVal, exists := c.Get("tenantConfig"); exists {
				capturedConfig = cfgVal.(tenantdb.Config)
			}
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Tenant-ID", "tenant-456")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}
		if capturedTenantID != "tenant-456" {
			t.Errorf("expected tenantID tenant-456, got '%s'", capturedTenantID)
		}
		if capturedConfig.SchemaName != expectedCfg.SchemaName {
			t.Errorf("expected SchemaName '%s', got '%s'", expectedCfg.SchemaName, capturedConfig.SchemaName)
		}
	})
}
