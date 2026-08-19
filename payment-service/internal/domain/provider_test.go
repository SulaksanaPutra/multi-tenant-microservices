package domain_test

import (
	"testing"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/testutil"
)

func TestWebhookEvent_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	evt := domain.WebhookEvent{
		EventID:           "evt_wh_1",
		EventType:         domain.WebhookEventTypePaymentSucceeded,
		Provider:          domain.ProviderStripe,
		TenantID:          "tnt_1",
		OrderID:           "ord_1",
		PaymentID:         "pay_1",
		ExternalSessionID: "sess_stripe_123",
		Amount:            99.99,
		Currency:          "USD",
		Timestamp:         now,
		RawPayload:        map[string]any{"status": "succeeded"},
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled.EventID != evt.EventID || unmarshaled.EventType != domain.WebhookEventTypePaymentSucceeded {
		t.Errorf("WebhookEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestWebhookEventType_Constants(t *testing.T) {
	types := []domain.WebhookEventType{
		domain.WebhookEventTypePaymentSucceeded,
		domain.WebhookEventTypePaymentFailed,
		domain.WebhookEventTypePaymentRefunded,
	}

	for _, typ := range types {
		if string(typ) == "" {
			t.Errorf("found empty WebhookEventType constant")
		}
	}
}

func TestProviderType_Constants(t *testing.T) {
	providers := []domain.ProviderType{
		domain.ProviderStripe,
		domain.ProviderXendit,
		domain.ProviderMidtrans,
		domain.ProviderDirectBank,
		domain.ProviderMock,
	}

	for _, p := range providers {
		if string(p) == "" {
			t.Errorf("found empty ProviderType constant")
		}
	}
}

func TestTenantPSPConfig_Struct(t *testing.T) {
	cfg := domain.TenantPSPConfig{
		TenantID: "tnt_1",
		Methods: []domain.PaymentMethodConfig{
			{
				ID:            "bca_va",
				Name:          "BCA VA",
				Type:          domain.InstructionVirtualAccount,
				Enabled:       true,
				PriorityChain: []domain.ProviderType{domain.ProviderStripe, domain.ProviderXendit},
			},
		},
		ProviderConfigs: map[domain.ProviderType]domain.ProviderCredentials{
			domain.ProviderStripe: {
				APIKey: "sk_test_123",
			},
		},
	}

	if cfg.TenantID != "tnt_1" {
		t.Errorf("Expected TenantID 'tnt_1', got '%s'", cfg.TenantID)
	}
	if len(cfg.Methods) != 1 {
		t.Errorf("Expected 1 method, got %d", len(cfg.Methods))
	}
	if cfg.ProviderConfigs[domain.ProviderStripe].APIKey != "sk_test_123" {
		t.Errorf("Expected Stripe API key 'sk_test_123'")
	}
}
