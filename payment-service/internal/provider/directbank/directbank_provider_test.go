package directbank

import (
	"context"
	"testing"

	"payment-service/internal/domain"
)

func TestDirectBankProvider_CreatePaymentSession(t *testing.T) {
	p := NewDirectBankProvider("BCA")

	if p.ID() != domain.ProviderDirectBank {
		t.Errorf("expected ID ProviderDirectBank, got %s", p.ID())
	}

	req := domain.CreateSessionRequest{
		PaymentID: "pay_123",
		TenantID:  "tnt_123",
		OrderID:   "ord_123",
		Amount:    100.00,
		Currency:  "USD",
	}

	res, err := p.CreatePaymentSession(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Provider != domain.ProviderDirectBank {
		t.Errorf("expected ProviderDirectBank, got %s", res.Provider)
	}
	if res.Instructions.BankCode != "BCA" {
		t.Errorf("expected BCA bank code, got %s", res.Instructions.BankCode)
	}

	if err := p.CancelPaymentSession(context.Background(), res.ExternalSessionID); err != nil {
		t.Errorf("expected cancel session to succeed, got %v", err)
	}
}

func TestDirectBankProvider_VerifyWebhookSignature(t *testing.T) {
	p := NewDirectBankProvider("BCA")

	body := []byte(`{
		"event_id": "evt_1",
		"tenant_id": "tnt_1",
		"order_id": "ord_1",
		"payment_id": "pay_1",
		"external_session_id": "va_BCA_pay_1",
		"amount": 100.0,
		"currency": "USD"
	}`)

	evt, err := p.VerifyWebhookSignature(context.Background(), nil, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if evt.EventID != "evt_1" || evt.Amount != 100.0 || evt.Currency != "USD" {
		t.Errorf("unexpected webhook event content: %+v", evt)
	}

	badBody := []byte(`invalid json`)
	if _, err := p.VerifyWebhookSignature(context.Background(), nil, badBody); err == nil {
		t.Error("expected error for invalid json body")
	}
}
