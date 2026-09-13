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

func (mockPayment *mockPaymentService) GetPaymentByID(ctx context.Context, id string) (*service.PaymentOutput, error) {
	if mockPayment.getByIDFn != nil {
		return mockPayment.getByIDFn(ctx, id)
	}
	return &service.PaymentOutput{ID: id, TenantID: "tnt_1"}, nil
}

func (mockPayment *mockPaymentService) GetPaymentByOrderID(ctx context.Context, tenantID, orderID string) (*service.PaymentOutput, error) {
	if mockPayment.getByOrderIDFn != nil {
		return mockPayment.getByOrderIDFn(ctx, tenantID, orderID)
	}
	return &service.PaymentOutput{ID: "pay_1", TenantID: tenantID, OrderID: orderID, Amount: 100.0, Currency: "USD", Status: domain.PaymentStatusPending}, nil
}

func (mockPayment *mockPaymentService) InitiatePaymentSession(ctx context.Context, input service.InitiatePaymentSessionInput) (*service.InitiatePaymentSessionOutput, error) {
	if mockPayment.initiateSessionFn != nil {
		return mockPayment.initiateSessionFn(ctx, input)
	}
	debt := &service.PayableDebtOutput{ID: "debt_1", TenantID: input.TenantID, OrderID: input.OrderID, TotalAmount: 100.0, Status: domain.DebtStatusUnpaid}
	pay := &service.PaymentOutput{ID: "pay_1", DebtID: "debt_1", TenantID: input.TenantID, OrderID: input.OrderID, Amount: 100.0, Currency: "USD", Status: domain.PaymentStatusPending}
	return &service.InitiatePaymentSessionOutput{
		Debt:    debt,
		Payment: pay,
	}, nil
}

func (mockPayment *mockPaymentService) CompleteInstructionGeneration(ctx context.Context, input service.CompleteInstructionInput) error {
	if mockPayment.completeFn != nil {
		return mockPayment.completeFn(ctx, input)
	}
	return nil
}

func (mockPayment *mockPaymentService) FailInstructionGeneration(ctx context.Context, input service.FailInstructionInput) error {
	if mockPayment.failFn != nil {
		return mockPayment.failFn(ctx, input)
	}
	return nil
}

func (mockPayment *mockPaymentService) ListAttemptsByPaymentID(ctx context.Context, paymentID string) ([]*service.PaymentAttemptOutput, error) {
	if mockPayment.listAttemptsFn != nil {
		return mockPayment.listAttemptsFn(ctx, paymentID)
	}
	return nil, nil
}

func (mockPayment *mockPaymentService) ProcessVerifiedWebhook(ctx context.Context, input service.ProcessVerifiedWebhookInput) (*service.ProcessWebhookOutput, error) {
	if mockPayment.processWebhookFn != nil {
		return mockPayment.processWebhookFn(ctx, input)
	}
	return &service.ProcessWebhookOutput{PaymentID: input.PaymentID, Status: domain.PaymentStatusSucceeded}, nil
}

type mockDebtService struct {
	getByOrderIDFn func(ctx context.Context, tenantID, orderID string) (*service.PayableDebtOutput, error)
	getByIDFn      func(ctx context.Context, id string) (*service.PayableDebtOutput, error)
}

func (mockDebt *mockDebtService) GetPayableDebtByOrderID(ctx context.Context, tenantID, orderID string) (*service.PayableDebtOutput, error) {
	if mockDebt.getByOrderIDFn != nil {
		return mockDebt.getByOrderIDFn(ctx, tenantID, orderID)
	}
	return &service.PayableDebtOutput{ID: "debt_1", TenantID: tenantID, OrderID: orderID, TotalAmount: 100.0, PaidAmount: 0, Currency: "USD", Status: domain.DebtStatusUnpaid}, nil
}

func (mockDebt *mockDebtService) GetPayableDebtByID(ctx context.Context, id string) (*service.PayableDebtOutput, error) {
	if mockDebt.getByIDFn != nil {
		return mockDebt.getByIDFn(ctx, id)
	}
	return &service.PayableDebtOutput{ID: id, TenantID: "tnt_1", TotalAmount: 100.0, PaidAmount: 0, Currency: "USD", Status: domain.DebtStatusUnpaid}, nil
}

type mockPaymentProviderService struct {
	getMethodsFn      func(ctx context.Context, tenantID string) ([]service.PaymentMethodOutput, error)
	executeFallbackFn func(ctx context.Context, input service.CreatePaymentSessionWithFallbackInput) (*service.CreatePaymentSessionWithFallbackOutput, error)
	verifyFn          func(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) (*service.VerifyWebhookOutput, error)
	cancelFn          func(ctx context.Context, providerID domain.ProviderType, externalSessionID string) error
}

func (mockProvider *mockPaymentProviderService) ListAvailablePaymentMethods(ctx context.Context, tenantID string) ([]service.PaymentMethodOutput, error) {
	if mockProvider.getMethodsFn != nil {
		return mockProvider.getMethodsFn(ctx, tenantID)
	}
	return []service.PaymentMethodOutput{
		{ID: "bca_va", Name: "BCA Virtual Account", Type: domain.InstructionVirtualAccount},
	}, nil
}

func (mockProvider *mockPaymentProviderService) CreatePaymentSessionWithFallback(ctx context.Context, input service.CreatePaymentSessionWithFallbackInput) (*service.CreatePaymentSessionWithFallbackOutput, error) {
	if mockProvider.executeFallbackFn != nil {
		return mockProvider.executeFallbackFn(ctx, input)
	}
	return &service.CreatePaymentSessionWithFallbackOutput{
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

func (mockProvider *mockPaymentProviderService) VerifyWebhookSignature(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) (*service.VerifyWebhookOutput, error) {
	if mockProvider.verifyFn != nil {
		return mockProvider.verifyFn(ctx, providerID, headers, body)
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

func (mockProvider *mockPaymentProviderService) CancelPaymentSession(ctx context.Context, providerID domain.ProviderType, externalSessionID string) error {
	if mockProvider.cancelFn != nil {
		return mockProvider.cancelFn(ctx, providerID, externalSessionID)
	}
	return nil
}

type mockPSPConfigService struct {
	saveFn func(ctx context.Context, input service.SavePSPConfigInput) error
	getFn  func(ctx context.Context, tenantID string) (*service.TenantPSPConfigOutput, error)
}

func (mockPSP *mockPSPConfigService) SaveConfig(ctx context.Context, input service.SavePSPConfigInput) error {
	if mockPSP.saveFn != nil {
		return mockPSP.saveFn(ctx, input)
	}
	return nil
}

func (mockPSP *mockPSPConfigService) GetConfig(ctx context.Context, tenantID string) (*service.TenantPSPConfigOutput, error) {
	if mockPSP.getFn != nil {
		return mockPSP.getFn(ctx, tenantID)
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

	httpReq, _ := http.NewRequest(http.MethodGet, "/api/payments/methods", nil)
	httpRecorder := httptest.NewRecorder()
	router.ServeHTTP(httpRecorder, httpReq)

	if httpRecorder.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", httpRecorder.Code)
	}

	var resp struct {
		Data []PaymentMethodResponse `json:"data"`
	}
	if err := json.Unmarshal(httpRecorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "bca_va" || resp.Data[0].Name != "BCA Virtual Account" {
		t.Errorf("unexpected methods response: %+v", resp.Data)
	}
}

func TestPaymentHandler_InitiatePayment(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	payload := InitiatePaymentSessionRequest{
		OrderID:       "ord_123",
		PaymentMethod: "bca_va",
	}
	body, _ := json.Marshal(payload)

	httpReq, _ := http.NewRequest(http.MethodPost, "/api/payments/initiate", bytes.NewBuffer(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpRecorder := httptest.NewRecorder()
	router.ServeHTTP(httpRecorder, httpReq)

	if httpRecorder.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", httpRecorder.Code)
	}
}

func TestPaymentHandler_GetPaymentByID(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	httpReq, _ := http.NewRequest(http.MethodGet, "/api/payments/pay_123", nil)
	httpRecorder := httptest.NewRecorder()
	router.ServeHTTP(httpRecorder, httpReq)

	if httpRecorder.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", httpRecorder.Code)
	}
}

func TestPaymentHandler_GetPaymentByOrderID(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	httpReq, _ := http.NewRequest(http.MethodGet, "/api/payments/by-order/ord_123", nil)
	httpRecorder := httptest.NewRecorder()
	router.ServeHTTP(httpRecorder, httpReq)

	if httpRecorder.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", httpRecorder.Code)
	}
}

func TestPaymentHandler_GetPayableDebtByOrderID(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	httpReq, _ := http.NewRequest(http.MethodGet, "/api/payments/debt/ord_123", nil)
	httpRecorder := httptest.NewRecorder()
	router.ServeHTTP(httpRecorder, httpReq)

	if httpRecorder.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", httpRecorder.Code)
	}
}

func TestPaymentHandler_HandleWebhook(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	body := []byte(`{"test": true}`)
	httpReq, _ := http.NewRequest(http.MethodPost, "/api/payments/webhook/mock", bytes.NewBuffer(body))
	httpRecorder := httptest.NewRecorder()
	router.ServeHTTP(httpRecorder, httpReq)

	if httpRecorder.Code != http.StatusOK {
		t.Errorf("expected status 200 for webhook, got %d", httpRecorder.Code)
	}
}

func TestPaymentHandler_UpdateAndGetPSPConfig(t *testing.T) {
	router := setupTestRouter(&mockPaymentService{}, &mockDebtService{}, &mockPaymentProviderService{}, &mockPSPConfigService{})

	payload := UpdatePSPConfigRequest{
		Methods: []PaymentMethodConfigRequest{
			{
				ID:            "bca_va",
				Name:          "BCA Virtual Account",
				Type:          string(domain.InstructionVirtualAccount),
				Enabled:       true,
				PriorityChain: []string{string(domain.ProviderDirectBank)},
			},
		},
	}
	bodyBytes, _ := json.Marshal(payload)

	httpReq, _ := http.NewRequest(http.MethodPut, "/api/payments/config", bytes.NewBuffer(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpRecorder := httptest.NewRecorder()
	router.ServeHTTP(httpRecorder, httpReq)

	if httpRecorder.Code != http.StatusOK {
		t.Errorf("expected status 200 for UpdatePSPConfig, got %d", httpRecorder.Code)
	}

	reqGet, _ := http.NewRequest(http.MethodGet, "/api/payments/config", nil)
	recorderGet := httptest.NewRecorder()
	router.ServeHTTP(recorderGet, reqGet)

	if recorderGet.Code != http.StatusOK {
		t.Errorf("expected status 200 for GetPSPConfig, got %d", recorderGet.Code)
	}
}
