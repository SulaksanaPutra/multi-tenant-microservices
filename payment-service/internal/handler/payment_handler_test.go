package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"payment-service/internal/domain"
	"payment-service/internal/service"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
)

type mockPaymentService struct {
	getByIDFn       func(ctx context.Context, id string) (*service.PaymentOutput, error)
	getByOrderIDFn  func(ctx context.Context, tenantID, orderID string) (*service.PaymentOutput, error)
	processWebhookFn func(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) error
	savePSPConfigFn  func(ctx context.Context, config *domain.TenantPSPConfig) error
	getPSPConfigFn   func(ctx context.Context, tenantID string) (*service.TenantPSPConfigOutput, error)
}

func (m *mockPaymentService) GetPaymentByID(ctx context.Context, id string) (*service.PaymentOutput, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id)
	}
	return &service.PaymentOutput{ID: id, TenantID: "tnt_1"}, nil
}

func (m *mockPaymentService) GetPaymentByOrderID(ctx context.Context, tenantID, orderID string) (*service.PaymentOutput, error) {
	if m.getByOrderIDFn != nil {
		return m.getByOrderIDFn(ctx, tenantID, orderID)
	}
	return &service.PaymentOutput{ID: "pay_1", TenantID: tenantID, OrderID: orderID}, nil
}

func (m *mockPaymentService) ProcessWebhook(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) error {
	if m.processWebhookFn != nil {
		return m.processWebhookFn(ctx, providerID, headers, body)
	}
	return nil
}

func (m *mockPaymentService) SavePSPConfig(ctx context.Context, config *domain.TenantPSPConfig) error {
	if m.savePSPConfigFn != nil {
		return m.savePSPConfigFn(ctx, config)
	}
	return nil
}

func (m *mockPaymentService) GetPSPConfig(ctx context.Context, tenantID string) (*service.TenantPSPConfigOutput, error) {
	if m.getPSPConfigFn != nil {
		return m.getPSPConfigFn(ctx, tenantID)
	}
	return &service.TenantPSPConfigOutput{TenantID: tenantID}, nil
}

func setupTestRouter(svc PaymentService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewPaymentHandler(svc)

	r.POST("/api/payments/webhook/:provider", h.HandleWebhook)

	api := r.Group("/api/payments")
	api.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyTenantID, "tnt_1")
		c.Next()
	})
	{
		api.GET("/:id", h.GetPaymentByID)
		api.GET("/by-order/:orderID", h.GetPaymentByOrderID)
		api.PUT("/config", h.UpdatePSPConfig)
		api.GET("/config", h.GetPSPConfig)
	}
	return r
}

func TestPaymentHandler_GetPaymentByID(t *testing.T) {
	svc := &mockPaymentService{}
	r := setupTestRouter(svc)

	req, _ := http.NewRequest(http.MethodGet, "/api/payments/pay_123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestPaymentHandler_GetPaymentByOrderID(t *testing.T) {
	svc := &mockPaymentService{}
	r := setupTestRouter(svc)

	req, _ := http.NewRequest(http.MethodGet, "/api/payments/by-order/ord_123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestPaymentHandler_HandleWebhook(t *testing.T) {
	svc := &mockPaymentService{}
	r := setupTestRouter(svc)

	body := []byte(`{"test": true}`)
	req, _ := http.NewRequest(http.MethodPost, "/api/payments/webhook/mock", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for webhook, got %d", w.Code)
	}
}

func TestPaymentHandler_UpdateAndGetPSPConfig(t *testing.T) {
	svc := &mockPaymentService{}
	r := setupTestRouter(svc)

	payload := UpdatePSPConfigRequest{
		PriorityChain: []domain.ProviderType{domain.ProviderMock},
	}
	b, _ := json.Marshal(payload)

	req, _ := http.NewRequest(http.MethodPut, "/api/payments/config", bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for UpdatePSPConfig, got %d", w.Code)
	}

	reqGet, _ := http.NewRequest(http.MethodGet, "/api/payments/config", nil)
	wGet := httptest.NewRecorder()
	r.ServeHTTP(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Errorf("expected status 200 for GetPSPConfig, got %d", wGet.Code)
	}
}
