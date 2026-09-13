package service_test

import (
	"context"
	"testing"

	"payment-service/internal/domain"
	"payment-service/internal/provider"
	"payment-service/internal/provider/mock"
	"payment-service/internal/service"
)

// mockRegistry satisfies the service.ProviderRegistry consumer-side interface.
type mockRegistry struct {
	mockProv *mock.MockProvider
}

func (mockReg *mockRegistry) CreatePaymentSessionWithFallback(ctx context.Context, cfg *domain.TenantPSPConfig, req domain.CreateSessionRequest, methodID string) (*provider.FallbackExecutionOutput, error) {
	session, err := mockReg.mockProv.CreatePaymentSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return &provider.FallbackExecutionOutput{
		Provider:      domain.ProviderMock,
		PaymentMethod: methodID,
		Session:       session,
	}, nil
}

func (mockReg *mockRegistry) IsHealthy(providerID domain.ProviderType) bool {
	return providerID == domain.ProviderMock
}

func (mockReg *mockRegistry) GetProvider(providerID domain.ProviderType) (domain.PaymentProvider, bool) {
	if providerID == domain.ProviderMock {
		return mockReg.mockProv, true
	}
	return nil, false
}

func newTestProviderService(registry *mockRegistry, pspConfigRepository *mockPSPConfigRepository) *service.PaymentProviderService {
	return service.NewPaymentProviderService(registry, pspConfigRepository, nil)
}

func TestPaymentProviderService_ListAvailablePaymentMethods(t *testing.T) {
	pspConfigRepository := &mockPSPConfigRepository{
		config: &domain.TenantPSPConfig{
			TenantID: "tnt_1",
			Methods: []domain.PaymentMethodConfig{
				{ID: "mock_checkout", Name: "Mock Checkout", Type: domain.InstructionRedirectURL, Enabled: true, PriorityChain: []domain.ProviderType{domain.ProviderMock}},
				{ID: "disabled_method", Name: "Disabled", Type: domain.InstructionVirtualAccount, Enabled: false, PriorityChain: []domain.ProviderType{domain.ProviderMock}},
				{ID: "dead_method", Name: "Dead Provider", Type: domain.InstructionQRIS, Enabled: true, PriorityChain: []domain.ProviderType{"dead_provider"}},
			},
		},
	}
	registry := &mockRegistry{mockProv: mock.NewMockProvider(domain.ProviderMock, "secret", false)}
	paymentProviderService := newTestProviderService(registry, pspConfigRepository)

	methods, err := paymentProviderService.ListAvailablePaymentMethods(context.Background(), "tnt_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only mock_checkout should survive: disabled_method is off, dead_method has no healthy provider.
	if len(methods) != 1 {
		t.Fatalf("expected 1 available method, got %d: %v", len(methods), methods)
	}
	if methods[0].ID != "mock_checkout" {
		t.Errorf("expected method ID mock_checkout, got %s", methods[0].ID)
	}
}

func TestPaymentProviderService_CreatePaymentSessionWithFallback(t *testing.T) {
	mockProv := mock.NewMockProvider(domain.ProviderMock, "secret", false)
	registry := &mockRegistry{mockProv: mockProv}
	pspConfigRepository := &mockPSPConfigRepository{}
	paymentProviderService := newTestProviderService(registry, pspConfigRepository)

	out, err := paymentProviderService.CreatePaymentSessionWithFallback(context.Background(), service.CreatePaymentSessionWithFallbackInput{
		TenantID:    "tnt_1",
		PaymentID:   "pay_1",
		OrderID:     "ord_1",
		Amount:      100.0,
		Currency:    "USD",
		Description: "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Provider != domain.ProviderMock {
		t.Errorf("expected Provider mock, got %s", out.Provider)
	}
	if out.Session.ExternalSessionID == "" {
		t.Error("expected non-empty ExternalSessionID")
	}
}

func TestPaymentProviderService_VerifyWebhookSignature(t *testing.T) {
	mockProv := mock.NewMockProvider(domain.ProviderMock, "secret", false)
	registry := &mockRegistry{mockProv: mockProv}
	pspConfigRepository := &mockPSPConfigRepository{}
	paymentProviderService := newTestProviderService(registry, pspConfigRepository)

	body := []byte(`{"event_id":"evt_1","event_type":"payment.succeeded","tenant_id":"tnt_1","order_id":"ord_1","payment_id":"pay_1","external_session_id":"ext_1","amount":100,"currency":"USD"}`)
	headers := map[string]string{"X-Webhook-Signature": "mock_hmac_signature"}

	evt, err := paymentProviderService.VerifyWebhookSignature(context.Background(), domain.ProviderMock, headers, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.EventID != "evt_1" {
		t.Errorf("expected EventID evt_1, got %s", evt.EventID)
	}
}

func TestPaymentProviderService_CancelPaymentSession(t *testing.T) {
	mockProv := mock.NewMockProvider(domain.ProviderMock, "secret", false)
	registry := &mockRegistry{mockProv: mockProv}
	pspConfigRepository := &mockPSPConfigRepository{}
	paymentProviderService := newTestProviderService(registry, pspConfigRepository)

	if err := paymentProviderService.CancelPaymentSession(context.Background(), domain.ProviderMock, "ext_1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
