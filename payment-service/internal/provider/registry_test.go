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
	resolver := NewDefaultTenantPSPResolver([]domain.ProviderType{
		"mock_primary",
		"mock_secondary",
		domain.ProviderDirectBank,
	})

	registry := NewProviderRegistry(resolver)

	// Primary mock returns transient error
	primary := mock.NewMockProvider("mock_primary", "secret", true)
	// Secondary mock succeeds
	secondary := mock.NewMockProvider("mock_secondary", "secret", false)
	// Direct bank succeeds
	bank := directbank.NewDirectBankProvider("BCA")

	registry.RegisterProvider(primary, 3, 30*time.Second)
	registry.RegisterProvider(secondary, 3, 30*time.Second)
	registry.RegisterProvider(bank, 3, 30*time.Second)

	req := domain.CreateSessionRequest{
		TenantID:  "tenant_test",
		PaymentID: "pay_123",
		OrderID:   "ord_123",
		Amount:    150.00,
		Currency:  "USD",
	}

	res, err := registry.ExecuteFallbackChain(context.Background(), req)
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
