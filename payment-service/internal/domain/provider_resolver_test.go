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
		TenantID: "tnt_99",
		Methods: []domain.PaymentMethodConfig{
			{
				ID:            "bca_va",
				Name:          "BCA Virtual Account",
				Type:          domain.InstructionVirtualAccount,
				Enabled:       true,
				PriorityChain: []domain.ProviderType{domain.ProviderDirectBank, domain.ProviderMock},
			},
		},
		ProviderConfigs: map[domain.ProviderType]domain.ProviderCredentials{
			domain.ProviderStripe: {
				APIKey: "sk_live_abc",
			},
		},
	}

	if cfg.TenantID != "tnt_99" {
		t.Errorf("Expected TenantID 'tnt_99', got '%s'", cfg.TenantID)
	}
	if len(cfg.Methods) != 1 {
		t.Errorf("Expected Methods length 1, got %d", len(cfg.Methods))
	}
	if cfg.ProviderConfigs[domain.ProviderStripe].APIKey != "sk_live_abc" {
		t.Errorf("Expected Stripe API Key 'sk_live_abc'")
	}
}
