package domain_test

import (
	"testing"

	"payment-service/internal/domain"
	"payment-service/internal/testutil"
)

func TestProviderCredentials_JSON(t *testing.T) {
	creds := domain.ProviderCredentials{
		APIKey:        "pk_test_123",
		SecretKey:     "sk_test_456",
		WebhookSecret: "whsec_789",
		ExtraOptions: map[string]string{
			"merchant_id": "m_001",
		},
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, creds)
	if unmarshaled.APIKey != creds.APIKey || unmarshaled.SecretKey != creds.SecretKey {
		t.Errorf("ProviderCredentials mismatch: %+v vs %+v", unmarshaled, creds)
	}
	if unmarshaled.ExtraOptions["merchant_id"] != "m_001" {
		t.Errorf("Expected extra_options merchant_id 'm_001', got '%s'", unmarshaled.ExtraOptions["merchant_id"])
	}
}

func TestTenantPSPConfig_Validation(t *testing.T) {
	cfg := domain.TenantPSPConfig{
		TenantID:      "tnt_99",
		PriorityChain: []domain.ProviderType{domain.ProviderStripe, domain.ProviderDirectBank},
		ProviderConfigs: map[domain.ProviderType]domain.ProviderCredentials{
			domain.ProviderStripe: {
				APIKey: "sk_live_abc",
			},
		},
	}

	if cfg.TenantID != "tnt_99" {
		t.Errorf("Expected TenantID 'tnt_99', got '%s'", cfg.TenantID)
	}
	if len(cfg.PriorityChain) != 2 {
		t.Errorf("Expected PriorityChain length 2, got %d", len(cfg.PriorityChain))
	}
	if cfg.ProviderConfigs[domain.ProviderStripe].APIKey != "sk_live_abc" {
		t.Errorf("Expected Stripe API Key 'sk_live_abc'")
	}
}
