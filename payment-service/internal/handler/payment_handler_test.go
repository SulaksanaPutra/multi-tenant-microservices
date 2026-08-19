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
	getByIDFn         func(ctx context.Context, id string) (*service.PaymentOutput, error)
	getByOrderIDFn    func(ctx context.Context, tenantID, orderID string) (*service.PaymentOutput, error)
	initiateSessionFn func(ctx context.Context, input service.InitiatePaymentSessionInput) (*service.InitiatePaymentSessionOutput, error)
	completeFn        func(ctx context.Context, input service.CompleteInstructionInput) error
	failFn            func(ctx context.Context, input service.FailInstructionInput) error
	listAttemptsFn    func(ctx context.Context, paymentID string) ([]*service.PaymentAttemptOutput, error)
	processWebhookFn  func(ctx context.Context, input service.ProcessVerifiedWebhookInput) (*service.ProcessWebhookOutput, error)
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
	return &service.PaymentOutput{ID: "pay_1", TenantID: tenantID, OrderID: orderID, Amount: 100.0, Currency: "USD", Status: domain.PaymentStatusPending}, nil
}

func (m *mockPaymentService) InitiatePaymentSession(ctx context.Context, input service.InitiatePaymentSessionInput) (*service.InitiatePaymentSessionOutput, error) {
	if m.initiateSessionFn != nil {
		return m.initiateSessionFn(ctx, input)
	}
	debt := &service.PayableDebtOutput{ID: "debt_1", TenantID: input.TenantID, OrderID: input.OrderID, TotalAmount: 100.0, Status: domain.DebtStatusUnpaid}
	pay := &service.PaymentOutput{ID: "pay_1", DebtID: "debt_1", TenantID: input.TenantID, OrderID: input.OrderID, Amount: 100.0, Currency: "USD", Status: domain.PaymentStatusPending}
	return &service.InitiatePaymentSessionOutput{
		Debt:    debt,
		Payment: pay,
	}, nil
}

func (m *mockPaymentService) CompleteInstructionGeneration(ctx context.Context, input service.CompleteInstructionInput) error {
	if m.completeFn != nil {
		return m.completeFn(ctx, input)
	}
	return nil
}

func (m *mockPaymentService) FailInstructionGeneration(ctx context.Context, input service.FailInstructionInput) error {
	if m.failFn != nil {
		return m.failFn(ctx, input)
	}
	return nil
}

func (m *mockPaymentService) ListAttemptsByPaymentID(ctx context.Context, paymentID string) ([]*service.PaymentAttemptOutput, error) {
	if m.listAttemptsFn != nil {
		return m.listAttemptsFn(ctx, paymentID)
	}
	return nil, nil
}

func (m *mockPaymentService) ProcessVerifiedWebhook(ctx context.Context, input service.ProcessVerifiedWebhookInput) (*service.ProcessWebhookOutput, error) {
	if m.processWebhookFn != nil {
		return m.processWebhookFn(ctx, input)
	}
	return &service.ProcessWebhookOutput{PaymentID: input.PaymentID, Status: domain.PaymentStatusSucceeded}, nil
}

type mockDebtService struct {
	getByOrderIDFn func(ctx context.Context, tenantID, orderID string) (*service.PayableDebtOutput, error)
	getByIDFn      func(ctx context.Context, id string) (*service.PayableDebtOutput, error)
}

func (m *mockDebtService) GetPayableDebtByOrderID(ctx context.Context, tenantID, orderID string) (*service.PayableDebtOutput, error) {
	if m.getByOrderIDFn != nil {
		return m.getByOrderIDFn(ctx, tenantID, orderID)
	}
	return &service.PayableDebtOutput{ID: "debt_1", TenantID: tenantID, OrderID: orderID, TotalAmount: 100.0, PaidAmount: 0, Currency: "USD", Status: domain.DebtStatusUnpaid}, nil
}

func (m *mockDebtService) GetPayableDebtByID(ctx context.Context, id string) (*service.PayableDebtOutput, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id)
	}
	return &service.PayableDebtOutput{ID: id, TenantID: "tnt_1", TotalAmount: 100.0, PaidAmount: 0, Currency: "USD", Status: domain.DebtStatusUnpaid}, nil
}

type mockPaymentProviderService struct {
	getMethodsFn      func(ctx context.Context, tenantID string) ([]service.PaymentMethodOutput, error)
	executeFallbackFn func(ctx context.Context, input service.ExecuteFallbackInput) (*service.ExecuteFallbackOutput, error)
	verifyFn          func(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) (*service.VerifyWebhookOutput, error)
	cancelFn          func(ctx context.Context, providerID domain.ProviderType, externalSessionID string) error
}

func (m *mockPaymentProviderService) ListAvailablePaymentMethods(ctx context.Context, tenantID string) ([]service.PaymentMethodOutput, error) {
	if m.getMethodsFn != nil {
		return m.getMethodsFn(ctx, tenantID)
	}
	return []service.PaymentMethodOutput{
		{ID: "bca_va", Name: "BCA Virtual Account", Type: domain.InstructionVirtualAccount},
	}, nil
}

func (m *mockPaymentProviderService) ExecuteFallback(ctx context.Context, input service.ExecuteFallbackInput) (*service.ExecuteFallbackOutput, error) {
	if m.executeFallbackFn != nil {
		return m.executeFallbackFn(ctx, input)
	}
	return &service.ExecuteFallbackOutput{
		Provider:      domain.ProviderDirectBank,
		PaymentMethod: input.PaymentMethod,
		Session: &domain.PaymentSessionOutput{
			Provider:          domain.ProviderDirectBank,
			ExternalSessionID: "ext_123",
			Instructions: domain.PaymentInstructions{
				Type:     domain.InstructionVirtualAccount,
				VANumber: "880123",
				BankCode: "BCA",
			},
		},
	}, nil
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
	debtService DebtService,
	paymentProviderService PaymentProviderService,
	pspConfigService PSPConfigService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	paymentHandler := NewPaymentHandler(paymentService, debtService, paymentProviderService, pspConfigService)

	router.POST("/api/payments/webhook/:provider", paymentHandler.HandleWebhook)

	api := router.Group("/api/payments")
	api.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyTenantID, "tnt_1")
		c.Next()
	})
	{
		api.GET("/methods", paymentHandler.ListAvailablePaymentMethods)
		api.POST("/initiate", paymentHandler.InitiatePayment)
		api.GET("/:id", paymentHandler.GetPaymentByID)
		api.GET("/by-order/:orderID", paymentHandler.GetPaymentByOrderID)
		api.GET("/debt/:orderID", paymentHandler.GetPayableDebtByOrderID)
		api.PUT("/config", paymentHandler.UpdatePSPConfig)
		api.GET("/config", paymentHandler.GetPSPConfig)
	}
	return router
}

func TestPaymentHandler_ListAvailablePaymentMethods(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	req, _ := http.NewRequest(http.MethodGet, "/api/payments/methods", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestPaymentHandler_InitiatePayment(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	payload := InitiatePaymentSessionRequest{
		OrderID:       "ord_123",
		PaymentMethod: "bca_va",
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest(http.MethodPost, "/api/payments/initiate", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestPaymentHandler_GetPaymentByID(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	req, _ := http.NewRequest(http.MethodGet, "/api/payments/pay_123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestPaymentHandler_GetPaymentByOrderID(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	req, _ := http.NewRequest(http.MethodGet, "/api/payments/by-order/ord_123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestPaymentHandler_GetPayableDebtByOrderID(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	req, _ := http.NewRequest(http.MethodGet, "/api/payments/debt/ord_123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestPaymentHandler_HandleWebhook(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	body := []byte(`{"test": true}`)
	req, _ := http.NewRequest(http.MethodPost, "/api/payments/webhook/mock", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for webhook, got %d", w.Code)
	}
}

func TestPaymentHandler_UpdateAndGetPSPConfig(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	payload := UpdatePSPConfigRequest{
		Methods: []domain.PaymentMethodConfig{
			{
				ID:            "bca_va",
				Name:          "BCA Virtual Account",
				Type:          domain.InstructionVirtualAccount,
				Enabled:       true,
				PriorityChain: []domain.ProviderType{domain.ProviderDirectBank},
			},
		},
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
