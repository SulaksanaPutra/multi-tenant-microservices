package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tenant-service/internal/domain"
	"tenant-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockTenantService struct {
	getTenantByIDFn    func(ctx context.Context, tenantID string) (*domain.Tenant, error)
	listTenantsFn      func(ctx context.Context, tenantID string) ([]domain.Tenant, error)
	updateTenantFn     func(ctx context.Context, input service.UpdateTenantServiceInput) error
	changeTenantPlanFn func(ctx context.Context, input service.ChangeTenantPlanInput) error
}

func (m *mockTenantService) GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error) {
	if m.getTenantByIDFn != nil {
		return m.getTenantByIDFn(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockTenantService) ListTenants(ctx context.Context, tenantID string) ([]domain.Tenant, error) {
	if m.listTenantsFn != nil {
		return m.listTenantsFn(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockTenantService) UpdateTenant(ctx context.Context, input service.UpdateTenantServiceInput) error {
	if m.updateTenantFn != nil {
		return m.updateTenantFn(ctx, input)
	}
	return nil
}

func (m *mockTenantService) ChangeTenantPlan(ctx context.Context, input service.ChangeTenantPlanInput) error {
	if m.changeTenantPlanFn != nil {
		return m.changeTenantPlanFn(ctx, input)
	}
	return nil
}

func setupTenantTestRouter(tenantHandler *TenantHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenantID", "ten_test123")
		c.Next()
	})
	r.GET("/api/tenants/me", tenantHandler.GetTenantMe)
	r.PUT("/api/tenants/me", tenantHandler.UpdateTenantMe)
	r.PUT("/api/tenants/me/plan", tenantHandler.ChangeTenantPlanMe)
	return r
}

func TestTenantHandler_GetTenantMe(t *testing.T) {
	mockSvc := &mockTenantService{
		getTenantByIDFn: func(ctx context.Context, tenantID string) (*domain.Tenant, error) {
			return &domain.Tenant{
				ID:         tenantID,
				Name:       "Acme Corp",
				Slug:       "acme-corp",
				OwnerEmail: "owner@example.com",
				Plan:       "shared",
			}, nil
		},
	}
	tenantHandler := NewTenantHandler(mockSvc)
	r := setupTenantTestRouter(tenantHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/tenants/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestTenantHandler_UpdateTenantMe(t *testing.T) {
	updated := false
	mockSvc := &mockTenantService{
		updateTenantFn: func(ctx context.Context, input service.UpdateTenantServiceInput) error {
			if input.TenantID == "ten_test123" && input.Name == "New Acme" {
				updated = true
			}
			return nil
		},
	}
	tenantHandler := NewTenantHandler(mockSvc)
	r := setupTenantTestRouter(tenantHandler)

	body, _ := json.Marshal(UpdateTenantRequest{
		Name:       "New Acme",
		Slug:       "new-acme",
		OwnerEmail: "owner@example.com",
		OwnerName:  "John Doe",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/tenants/me", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !updated {
		t.Fatalf("expected UpdateTenant to be called with correct payload")
	}
}

func TestTenantHandler_ChangeTenantPlanMe(t *testing.T) {
	changed := false
	mockSvc := &mockTenantService{
		changeTenantPlanFn: func(ctx context.Context, input service.ChangeTenantPlanInput) error {
			if input.TenantID == "ten_test123" && input.Plan == "dedicated" {
				changed = true
			}
			return nil
		},
	}
	tenantHandler := NewTenantHandler(mockSvc)
	r := setupTenantTestRouter(tenantHandler)

	body, _ := json.Marshal(ChangePlanRequest{Plan: "dedicated"})
	req := httptest.NewRequest(http.MethodPut, "/api/tenants/me/plan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !changed {
		t.Fatalf("expected ChangeTenantPlan to be called with dedicated plan")
	}
}
