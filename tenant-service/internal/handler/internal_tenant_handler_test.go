package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"tenant-service/internal/domain"

	"github.com/gin-gonic/gin"
)

type mockProfileProvider struct {
	getTenantByIDFn func(ctx context.Context, tenantID string) (*domain.Tenant, error)
}

func (m *mockProfileProvider) GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error) {
	if m.getTenantByIDFn != nil {
		return m.getTenantByIDFn(ctx, tenantID)
	}
	return nil, errors.New("not implemented")
}

func setupInternalTenantTestRouter(h *InternalTenantHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/internal/tenants/:tenant_id/profile", h.GetTenantProfile)
	return r
}

func TestInternalTenantHandler_GetTenantProfile(t *testing.T) {
	p := &mockProfileProvider{
		getTenantByIDFn: func(_ context.Context, tenantID string) (*domain.Tenant, error) {
			if tenantID != "ten_123" {
				return nil, errors.New("tenant not found")
			}
			return &domain.Tenant{
				ID:     "ten_123",
				Name:   "Acme Corp",
				Slug:   "acme-corp",
				Plan:   "community",
				Status: "ACTIVE",
			}, nil
		},
	}

	underTest := NewInternalTenantHandler(nil, p)
	r := setupInternalTenantTestRouter(underTest)

	req := httptest.NewRequest(http.MethodGet, "/internal/tenants/ten_123/profile", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, got %v", body)
	}

	if data["name"] != "Acme Corp" {
		t.Errorf("expected name Acme Corp, got %v", data["name"])
	}
	if data["slug"] != "acme-corp" {
		t.Errorf("expected slug acme-corp, got %v", data["slug"])
	}
	if data["plan"] != "community" {
		t.Errorf("expected plan community, got %v", data["plan"])
	}
}

func TestInternalTenantHandler_GetTenantProfile_NotFound(t *testing.T) {
	p := &mockProfileProvider{
		getTenantByIDFn: func(_ context.Context, _ string) (*domain.Tenant, error) {
			return nil, errors.New("tenant not found")
		},
	}

	underTest := NewInternalTenantHandler(nil, p)
	r := setupInternalTenantTestRouter(underTest)

	req := httptest.NewRequest(http.MethodGet, "/internal/tenants/ten_missing/profile", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}