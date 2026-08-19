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
	resolver := NewDefaultTenantPSPResolver([]domain.PaymentMethodConfig{
		{
			ID:            "bca_va",
			Name:          "BCA Virtual Account",
			Type:          domain.InstructionVirtualAccount,
			Enabled:       true,
			PriorityChain: []domain.ProviderType{"mock_primary", "mock_secondary", domain.ProviderDirectBank},
		},
	})

	registry := NewProviderRegistry(resolver)

	primary := mock.NewMockProvider("mock_primary", "secret", true)
	secondary := mock.NewMockProvider("mock_secondary", "secret", false)
	bank := directbank.NewDirectBankProvider("BCA")

	registry.RegisterProvider(primary, 3, 30*time.Second)
	registry.RegisterProvider(secondary, 3, 30*time.Second)
	registry.RegisterProvider(bank, 3, 30*time.Second)

	req := domain.CreateSessionRequest{
		TenantID:      "tenant_test",
		PaymentID:     "pay_123",
		OrderID:       "ord_123",
		Amount:        150.00,
		Currency:      "USD",
		PaymentMethod: "bca_va",
	}

	res, err := registry.ExecuteFallbackChain(context.Background(), req, "bca_va")
	if err != nil {
		t.Fatalf("expected fallback chain to succeed, got err: %v", err)
	}

	if res.Provider != "mock_secondary" {
		t.Fatalf("expected secondary provider to handle request, got: %s", res.Provider)
	}

	if len(res.FailedAttempts) != 1 || res.FailedAttempts[0] != "mock_primary" {
		t.Fatalf("expected mock_primary in failed attempts, got: %v", res.FailedAttempts)
	}

	methods, err := registry.GetAvailableMethods(context.Background(), "tenant_test")
	if err != nil {
		t.Fatalf("expected GetAvailableMethods to succeed, got err: %v", err)
	}
	if len(methods) != 1 || methods[0].ID != "bca_va" {
		t.Fatalf("unexpected available methods: %v", methods)
	}
}

func TestProviderRegistry_UnconfiguredTenant_EmptyMethods(t *testing.T) {
	resolver := NewDefaultTenantPSPResolver(nil)
	registry := NewProviderRegistry(resolver)

	methods, err := registry.GetAvailableMethods(context.Background(), "unconfigured_tenant")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(methods) != 0 {
		t.Fatalf("expected 0 methods for unconfigured tenant, got %d", len(methods))
	}

	req := domain.CreateSessionRequest{
		TenantID:      "unconfigured_tenant",
		PaymentID:     "pay_999",
		OrderID:       "ord_999",
		Amount:        100.0,
		Currency:      "USD",
		PaymentMethod: "bca_va",
	}

	_, err = registry.ExecuteFallbackChain(context.Background(), req, "bca_va")
	if err == nil {
		t.Fatal("expected error executing fallback on unconfigured method, got nil")
	}
	if err != domain.ErrInvalidPaymentMethod {
		t.Fatalf("expected ErrInvalidPaymentMethod, got %v", err)
	}
}
