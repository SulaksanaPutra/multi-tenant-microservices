package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tenant-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockTenantService struct {
	getTenantByIDFn    func(ctx context.Context, tenantID string) (*service.TenantOutput, error)
	updateTenantFn     func(ctx context.Context, input service.UpdateTenantServiceInput) error
	changeTenantPlanFn func(ctx context.Context, input service.ChangeTenantPlanInput) error
}

func (m *mockTenantService) GetTenantByID(ctx context.Context, tenantID string) (*service.TenantOutput, error) {
	if m.getTenantByIDFn != nil {
		return m.getTenantByIDFn(ctx, tenantID)
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
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenantID", "ten_test123")
		c.Next()
	})
	router.GET("/api/tenants/me", tenantHandler.GetTenantMe)
	router.PUT("/api/tenants/me", tenantHandler.UpdateTenantMe)
	router.PUT("/api/tenants/me/plan", tenantHandler.ChangeTenantPlanMe)
	return router
}

func TestTenantHandler_GetTenantMe(t *testing.T) {
	mockTenantService := &mockTenantService{
		getTenantByIDFn: func(ctx context.Context, tenantID string) (*service.TenantOutput, error) {
			return &service.TenantOutput{
				ID:         tenantID,
				Name:       "Acme Corp",
				Slug:       "acme-corp",
				OwnerEmail: "owner@example.com",
				Plan:       "shared",
			}, nil
		},
	}
	tenantHandler := NewTenantHandler(mockTenantService)
	router := setupTenantTestRouter(tenantHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/tenants/me", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp struct {
		Data TenantResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if resp.Data.TenantID != "ten_test123" {
		t.Errorf("expected tenant_id in response DTO, got %q", resp.Data.TenantID)
	}
	if resp.Data.Name != "Acme Corp" {
		t.Errorf("expected name in response DTO, got %q", resp.Data.Name)
	}
	if resp.Data.Slug != "acme-corp" {
		t.Errorf("expected slug in response DTO, got %q", resp.Data.Slug)
	}
	if resp.Data.OwnerEmail != "owner@example.com" {
		t.Errorf("expected owner_email in response DTO, got %q", resp.Data.OwnerEmail)
	}
	if resp.Data.Plan != "shared" {
		t.Errorf("expected plan in response DTO, got %q", resp.Data.Plan)
	}
}

func TestTenantHandler_UpdateTenantMe(t *testing.T) {
	updated := false
	mockTenantService := &mockTenantService{
		getTenantByIDFn: func(ctx context.Context, tenantID string) (*service.TenantOutput, error) {
			return &service.TenantOutput{
				ID:         "ten_test123",
				Name:       "New Acme",
				Slug:       "new-acme",
				OwnerEmail: "owner@example.com",
				Plan:       "shared",
			}, nil
		},
		updateTenantFn: func(ctx context.Context, input service.UpdateTenantServiceInput) error {
			if input.TenantID == "ten_test123" && input.Name == "New Acme" {
				updated = true
			}
			return nil
		},
	}
	tenantHandler := NewTenantHandler(mockTenantService)
	router := setupTenantTestRouter(tenantHandler)

	ownerEmail := "owner@example.com"
	body, _ := json.Marshal(UpdateTenantRequest{
		Name:       "New Acme",
		Slug:       "new-acme",
		OwnerEmail: &ownerEmail,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/tenants/me", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !updated {
		t.Fatalf("expected UpdateTenant to be called with correct payload")
	}
}

func TestTenantHandler_UpdateTenantMe_OwnerFieldsOptional(t *testing.T) {
	updated := false
	mockTenantService := &mockTenantService{
		getTenantByIDFn: func(ctx context.Context, tenantID string) (*service.TenantOutput, error) {
			return &service.TenantOutput{
				ID:         "ten_test123",
				Name:       "New Acme",
				Slug:       "new-acme",
				OwnerEmail: "owner@example.com",
				Plan:       "shared",
			}, nil
		},
		updateTenantFn: func(ctx context.Context, input service.UpdateTenantServiceInput) error {
			if input.TenantID == "ten_test123" && input.Name == "New Acme" && input.OwnerEmail == nil && input.OwnerName == nil {
				updated = true
			}
			return nil
		},
	}
	tenantHandler := NewTenantHandler(mockTenantService)
	router := setupTenantTestRouter(tenantHandler)

	body, _ := json.Marshal(UpdateTenantRequest{
		Name: "New Acme",
		Slug: "new-acme",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/tenants/me", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !updated {
		t.Fatalf("expected UpdateTenant to be called without owner fields")
	}
}

func TestTenantHandler_ChangeTenantPlanMe(t *testing.T) {
	changed := false
	mockTenantService := &mockTenantService{
		changeTenantPlanFn: func(ctx context.Context, input service.ChangeTenantPlanInput) error {
			if input.TenantID == "ten_test123" && input.Plan == "dedicated" {
				changed = true
			}
			return nil
		},
	}
	tenantHandler := NewTenantHandler(mockTenantService)
	router := setupTenantTestRouter(tenantHandler)

	body, _ := json.Marshal(ChangePlanRequest{Plan: "dedicated"})
	req := httptest.NewRequest(http.MethodPut, "/api/tenants/me/plan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !changed {
		t.Fatalf("expected ChangeTenantPlan to be called with dedicated plan")
	}

	var resp struct {
		Data ChangeTenantPlanResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if resp.Data.TenantID != "ten_test123" {
		t.Errorf("expected tenant_id in plan response DTO, got %q", resp.Data.TenantID)
	}
	if resp.Data.Plan != "dedicated" {
		t.Errorf("expected plan in plan response DTO, got %q", resp.Data.Plan)
	}
}
