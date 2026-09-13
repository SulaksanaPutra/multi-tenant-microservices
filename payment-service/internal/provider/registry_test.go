package provider

import (
	"context"
	"testing"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/provider/directbank"
	"payment-service/internal/provider/mock"
)

func TestProviderRegistry_FallbackChain(t *testing.T) {
	cfg := &domain.TenantPSPConfig{
		TenantID: "tenant_test",
		Methods: []domain.PaymentMethodConfig{
			{
				ID:            "bca_va",
				Name:          "BCA Virtual Account",
				Type:          domain.InstructionVirtualAccount,
				Enabled:       true,
				PriorityChain: []domain.ProviderType{"mock_primary", "mock_secondary", domain.ProviderDirectBank},
			},
		},
	}

	providerRegistry := NewProviderRegistry()

	primary := mock.NewMockProvider("mock_primary", "secret", true)
	secondary := mock.NewMockProvider("mock_secondary", "secret", false)
	bank := directbank.NewDirectBankProvider("BCA")

	providerRegistry.RegisterProvider(primary, 3, 30*time.Second)
	providerRegistry.RegisterProvider(secondary, 3, 30*time.Second)
	providerRegistry.RegisterProvider(bank, 3, 30*time.Second)

	req := domain.CreateSessionRequest{
		TenantID:      "tenant_test",
		PaymentID:     "pay_123",
		OrderID:       "ord_123",
		Amount:        150.00,
		Currency:      "USD",
		PaymentMethod: "bca_va",
	}

	res, err := providerRegistry.CreatePaymentSessionWithFallback(context.Background(), cfg, req, "bca_va")
	if err != nil {
		t.Fatalf("expected fallback chain to succeed, got err: %v", err)
	}

	if res.Provider != "mock_secondary" {
		t.Fatalf("expected secondary provider to handle request, got: %s", res.Provider)
	}

	if len(res.FailedAttempts) != 1 || res.FailedAttempts[0] != "mock_primary" {
		t.Fatalf("expected mock_primary in failed attempts, got: %v", res.FailedAttempts)
	}
}

func TestProviderRegistry_IsHealthy(t *testing.T) {
	providerRegistry := NewProviderRegistry()

	primary := mock.NewMockProvider("mock_primary", "secret", false)
	providerRegistry.RegisterProvider(primary, 1, 30*time.Second)

	if !providerRegistry.IsHealthy("mock_primary") {
		t.Fatal("expected mock_primary to be healthy after registration")
	}
	if providerRegistry.IsHealthy("not_registered") {
		t.Fatal("expected unregistered provider to be unhealthy")
	}
}

func TestProviderRegistry_UnconfiguredTenant_EmptyMethods(t *testing.T) {
	providerRegistry := NewProviderRegistry()

	emptyCfg := &domain.TenantPSPConfig{
		TenantID:        "unconfigured_tenant",
		Methods:         []domain.PaymentMethodConfig{},
		ProviderConfigs: make(map[domain.ProviderType]domain.ProviderCredentials),
	}

	req := domain.CreateSessionRequest{
		TenantID:      "unconfigured_tenant",
		PaymentID:     "pay_999",
		OrderID:       "ord_999",
		Amount:        100.0,
		Currency:      "USD",
		PaymentMethod: "bca_va",
	}

	_, err := providerRegistry.CreatePaymentSessionWithFallback(context.Background(), emptyCfg, req, "bca_va")
	if err == nil {
		t.Fatal("expected error executing fallback on unconfigured method, got nil")
	}
	if err != domain.ErrInvalidPaymentMethod {
		t.Fatalf("expected ErrInvalidPaymentMethod, got %v", err)
	}
}
