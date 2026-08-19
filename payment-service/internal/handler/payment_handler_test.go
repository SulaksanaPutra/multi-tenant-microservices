package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
	"payment-service/internal/domain"
	"payment-service/internal/service"
)

type mockPaymentService struct {
	getByIDFn        func(ctx context.Context, id string) (*service.PaymentOutput, error)
	getByOrderIDFn   func(ctx context.Context, tenantID, orderID string) (*service.PaymentOutput, error)
	processWebhookFn func(ctx context.Context, input service.ProcessVerifiedWebhookInput) (*service.ProcessWebhookOutput, error)
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

func (m *mockPaymentService) ProcessVerifiedWebhook(ctx context.Context, input service.ProcessVerifiedWebhookInput) (*service.ProcessWebhookOutput, error) {
	if m.processWebhookFn != nil {
		return m.processWebhookFn(ctx, input)
	}
	return &service.ProcessWebhookOutput{PaymentID: input.PaymentID, Status: domain.PaymentStatusSucceeded}, nil
}

type mockPaymentProviderService struct {
	verifyFn func(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) (*service.VerifyWebhookOutput, error)
	cancelFn func(ctx context.Context, providerID domain.ProviderType, externalSessionID string) error
}

func (m *mockPaymentProviderService) VerifyWebhookSignature(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) (*service.VerifyWebhookOutput, error) {
	if m.verifyFn != nil {
		return m.verifyFn(ctx, providerID, headers, body)
	}
	return &service.VerifyWebhookOutput{
		EventID:           "evt_1",
		EventType:         domain.WebhookEventTypePaymentSucceeded,
		Provider:          providerID,
		TenantID:          "tnt_1",
		OrderID:           "ord_1",
		PaymentID:         "pay_1",
		ExternalSessionID: "ext_1",
		Amount:            100.0,
		Currency:          "USD",
	}, nil
}

func (m *mockPaymentProviderService) CancelPaymentSession(ctx context.Context, providerID domain.ProviderType, externalSessionID string) error {
	if m.cancelFn != nil {
		return m.cancelFn(ctx, providerID, externalSessionID)
	}
	return nil
}

type mockPSPConfigService struct {
	saveFn func(ctx context.Context, input service.SavePSPConfigInput) error
	getFn  func(ctx context.Context, tenantID string) (*service.TenantPSPConfigOutput, error)
}

func (m *mockPSPConfigService) SaveConfig(ctx context.Context, input service.SavePSPConfigInput) error {
	if m.saveFn != nil {
		return m.saveFn(ctx, input)
	}
	return nil
}

func (m *mockPSPConfigService) GetConfig(ctx context.Context, tenantID string) (*service.TenantPSPConfigOutput, error) {
	if m.getFn != nil {
		return m.getFn(ctx, tenantID)
	}
	return &service.TenantPSPConfigOutput{TenantID: tenantID}, nil
}

func setupTestRouter(
	paymentService PaymentService,
	paymentProviderService PaymentProviderService,
	pspConfigService PSPConfigService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	paymentHandler := NewPaymentHandler(paymentService, paymentProviderService, pspConfigService)

	router.POST("/api/payments/webhook/:provider", paymentHandler.HandleWebhook)

	api := router.Group("/api/payments")
	api.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyTenantID, "tnt_1")
		c.Next()
	})
	{
		api.GET("/:id", paymentHandler.GetPaymentByID)
		api.GET("/by-order/:orderID", paymentHandler.GetPaymentByOrderID)
		api.PUT("/config", paymentHandler.UpdatePSPConfig)
		api.GET("/config", paymentHandler.GetPSPConfig)
	}
	return router
}

func TestPaymentHandler_GetPaymentByID(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	req, _ := http.NewRequest(http.MethodGet, "/api/payments/pay_123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestPaymentHandler_GetPaymentByOrderID(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	req, _ := http.NewRequest(http.MethodGet, "/api/payments/by-order/ord_123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestPaymentHandler_HandleWebhook(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	body := []byte(`{"test": true}`)
	req, _ := http.NewRequest(http.MethodPost, "/api/payments/webhook/mock", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for webhook, got %d", w.Code)
	}
}

func TestPaymentHandler_UpdateAndGetPSPConfig(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	payload := UpdatePSPConfigRequest{
		PriorityChain: []domain.ProviderType{domain.ProviderMock},
	}
	bodyBytes, _ := json.Marshal(payload)

	req, _ := http.NewRequest(http.MethodPut, "/api/payments/config", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for UpdatePSPConfig, got %d", w.Code)
	}

	reqGet, _ := http.NewRequest(http.MethodGet, "/api/payments/config", nil)
	wGet := httptest.NewRecorder()
	router.ServeHTTP(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Errorf("expected status 200 for GetPSPConfig, got %d", wGet.Code)
	}
}
