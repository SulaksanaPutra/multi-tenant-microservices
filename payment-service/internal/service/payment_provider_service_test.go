package service_test

import (
	"context"
	"testing"

	"payment-service/internal/domain"
	"payment-service/internal/provider"
	"payment-service/internal/provider/mock"
	"payment-service/internal/service"
)

type mockRegistry struct {
	mockProv *mock.MockProvider
}

func (m *mockRegistry) ExecuteFallbackChain(ctx context.Context, req domain.CreateSessionRequest) (*provider.FallbackExecutionOutput, error) {
	session, err := m.mockProv.CreatePaymentSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return &provider.FallbackExecutionOutput{
		Provider: domain.ProviderMock,
		Session:  session,
	}, nil
}

func (m *mockRegistry) GetProvider(providerID domain.ProviderType) (domain.PaymentProvider, bool) {
	if providerID == domain.ProviderMock {
		return m.mockProv, true
	}
	return nil, false
}

func TestPaymentProviderService_ExecuteFallback(t *testing.T) {
	mockProv := mock.NewMockProvider(domain.ProviderMock, "secret", false)
	paymentProviderService := service.NewPaymentProviderService(&mockRegistry{mockProv: mockProv}, nil)

	out, err := paymentProviderService.ExecuteFallback(context.Background(), service.ExecuteFallbackInput{
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
	paymentProviderService := service.NewPaymentProviderService(&mockRegistry{mockProv: mockProv}, nil)

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
	paymentProviderService := service.NewPaymentProviderService(&mockRegistry{mockProv: mockProv}, nil)

	if err := paymentProviderService.CancelPaymentSession(context.Background(), domain.ProviderMock, "ext_1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
