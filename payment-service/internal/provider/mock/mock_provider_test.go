package mock

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"payment-service/internal/domain"
)

func TestMockProvider_CreatePaymentSession(t *testing.T) {
	p := NewMockProvider(domain.ProviderMock, "mock_secret", false)

	if p.ID() != domain.ProviderMock {
		t.Errorf("expected ProviderMock, got %s", p.ID())
	}

	res, err := p.CreatePaymentSession(context.Background(), domain.CreateSessionRequest{
		PaymentID: "pay_99",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.ExternalSessionID != "ext_mock_pay_99" {
		t.Errorf("unexpected external session ID: %s", res.ExternalSessionID)
	}

	errProv := NewMockProvider(domain.ProviderMock, "mock_secret", true)
	if _, err := errProv.CreatePaymentSession(context.Background(), domain.CreateSessionRequest{}); !errors.Is(err, domain.ErrProviderTransientFailure) {
		t.Errorf("expected ErrProviderTransientFailure, got %v", err)
	}

	if err := p.CancelPaymentSession(context.Background(), res.ExternalSessionID); err != nil {
		t.Errorf("expected cancel session to succeed, got %v", err)
	}
}

func TestMockProvider_VerifyWebhookSignature(t *testing.T) {
	secret := "test_secret"
	p := NewMockProvider(domain.ProviderMock, secret, false)

	payload := map[string]any{
		"event_id":   "evt_mock_1",
		"event_type": "payment.succeeded",
		"tenant_id":  "tnt_1",
		"order_id":   "ord_1",
		"payment_id": "pay_1",
		"amount":     150.0,
		"currency":   "USD",
	}
	body, _ := json.Marshal(payload)
	sig := computeHMAC(body, secret)

	headers := map[string]string{
		"X-Webhook-Signature": sig,
	}

	evt, err := p.VerifyWebhookSignature(context.Background(), headers, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if evt.Amount != 150.0 || evt.EventType != domain.WebhookEventTypePaymentSucceeded {
		t.Errorf("unexpected event content: %+v", evt)
	}

	badHeaders := map[string]string{
		"X-Webhook-Signature": "invalid_sig",
	}
	if _, err := p.VerifyWebhookSignature(context.Background(), badHeaders, body); !errors.Is(err, domain.ErrInvalidWebhookSignature) {
		t.Errorf("expected ErrInvalidWebhookSignature, got %v", err)
	}
}
