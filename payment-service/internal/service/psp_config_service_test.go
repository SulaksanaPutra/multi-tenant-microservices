package service_test

import (
	"context"
	"testing"

	"payment-service/internal/domain"
	"payment-service/internal/repository"
	"payment-service/internal/service"
)

type mockPSPConfigRepository struct {
	saveFn func(ctx context.Context, input repository.SaveConfigInput, masterKey []byte) error
	getFn  func(ctx context.Context, tenantID string, masterKey []byte) (*domain.TenantPSPConfig, error)
}

func (m *mockPSPConfigRepository) SaveConfig(ctx context.Context, input repository.SaveConfigInput, masterKey []byte) error {
	if m.saveFn != nil {
		return m.saveFn(ctx, input, masterKey)
	}
	return nil
}

func (m *mockPSPConfigRepository) GetConfig(ctx context.Context, tenantID string, masterKey []byte) (*domain.TenantPSPConfig, error) {
	if m.getFn != nil {
		return m.getFn(ctx, tenantID, masterKey)
	}
	return &domain.TenantPSPConfig{
		TenantID: tenantID,
		Methods: []domain.PaymentMethodConfig{
			{ID: "mock_checkout", Name: "Mock", Type: domain.InstructionRedirectURL, Enabled: true, PriorityChain: []domain.ProviderType{domain.ProviderMock}},
		},
	}, nil
}

type mockTenantPSPResolver struct {
	invalidated bool
}

func (m *mockTenantPSPResolver) ResolveConfig(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error) {
	return &domain.TenantPSPConfig{
		TenantID: tenantID,
		Methods: []domain.PaymentMethodConfig{
			{ID: "mock_checkout", Name: "Mock", Type: domain.InstructionRedirectURL, Enabled: true, PriorityChain: []domain.ProviderType{domain.ProviderMock}},
		},
	}, nil
}

func (m *mockTenantPSPResolver) InvalidateCache(tenantID string) {
	m.invalidated = true
}

func TestPSPConfigService_SaveConfig(t *testing.T) {
	resolver := &mockTenantPSPResolver{}
	pspConfigService := service.NewPSPConfigService(nil, &mockPSPConfigRepository{}, resolver, []byte("01234567890123456789012345678901"), nil)

	if err := pspConfigService.SaveConfig(context.Background(), service.SavePSPConfigInput{}); err == nil {
		t.Error("expected error for empty tenant_id")
	}

	input := service.SavePSPConfigInput{
		TenantID: "tnt_100",
		Methods: []domain.PaymentMethodConfig{
			{ID: "mock_checkout", Name: "Mock", Type: domain.InstructionRedirectURL, Enabled: true, PriorityChain: []domain.ProviderType{domain.ProviderMock}},
		},
	}
	if err := pspConfigService.SaveConfig(context.Background(), input); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resolver.invalidated {
		t.Error("expected resolver cache invalidation")
	}
}

func TestPSPConfigService_GetConfig(t *testing.T) {
	pspConfigService := service.NewPSPConfigService(nil, &mockPSPConfigRepository{}, &mockTenantPSPResolver{}, []byte("01234567890123456789012345678901"), nil)

	cfg, err := pspConfigService.GetConfig(context.Background(), "tnt_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TenantID != "tnt_1" {
		t.Errorf("expected TenantID tnt_1, got %s", cfg.TenantID)
	}
}
