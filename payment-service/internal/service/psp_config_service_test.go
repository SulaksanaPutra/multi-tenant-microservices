package service_test

import (
	"context"
	"testing"

	"payment-service/internal/domain"
	"payment-service/internal/repository"
	"payment-service/internal/service"
)

type mockPSPConfigRepository struct {
	saveFn      func(ctx context.Context, input repository.SaveConfigInput) error
	getFn       func(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error)
	config      *domain.TenantPSPConfig
	invalidated bool
}

func (mockPSPConfigRepository *mockPSPConfigRepository) SaveConfig(ctx context.Context, input repository.SaveConfigInput) error {
	if mockPSPConfigRepository.saveFn != nil {
		return mockPSPConfigRepository.saveFn(ctx, input)
	}
	return nil
}

func (mockPSPConfigRepository *mockPSPConfigRepository) FindByTenantID(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error) {
	if mockPSPConfigRepository.getFn != nil {
		return mockPSPConfigRepository.getFn(ctx, tenantID)
	}
	if mockPSPConfigRepository.config != nil {
		return mockPSPConfigRepository.config, nil
	}
	return &domain.TenantPSPConfig{
		TenantID: tenantID,
		Methods: []domain.PaymentMethodConfig{
			{ID: "mock_checkout", Name: "Mock", Type: domain.InstructionRedirectURL, Enabled: true, PriorityChain: []domain.ProviderType{domain.ProviderMock}},
		},
		ProviderConfigs: make(map[domain.ProviderType]domain.ProviderCredentials),
	}, nil
}

func (mockPSPConfigRepository *mockPSPConfigRepository) InvalidateCache(tenantID string) {
	mockPSPConfigRepository.invalidated = true
}

func TestPSPConfigService_SaveConfig(t *testing.T) {
	pspConfigRepository := &mockPSPConfigRepository{}
	pspConfigService := service.NewPSPConfigService(nil, pspConfigRepository, []byte("01234567890123456789012345678901"), nil)

	if err := pspConfigService.SaveConfig(context.Background(), service.SavePSPConfigInput{}); err == nil {
		t.Fatal("expected error on empty tenant_id, got nil")
	}

	validInput := service.SavePSPConfigInput{
		TenantID: "tenant_1",
		Methods: []domain.PaymentMethodConfig{
			{ID: "mock_checkout", Name: "Mock", Type: domain.InstructionRedirectURL, Enabled: true},
		},
	}

	if err := pspConfigService.SaveConfig(context.Background(), validInput); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !pspConfigRepository.invalidated {
		t.Fatal("expected InvalidateCache to have been called on save")
	}
}

func TestPSPConfigService_GetConfig(t *testing.T) {
	pspConfigRepository := &mockPSPConfigRepository{}
	pspConfigService := service.NewPSPConfigService(nil, pspConfigRepository, []byte("01234567890123456789012345678901"), nil)

	if _, err := pspConfigService.GetConfig(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty tenant_id, got nil")
	}

	cfg, err := pspConfigService.GetConfig(context.Background(), "tenant_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TenantID != "tenant_1" {
		t.Fatalf("expected tenant_1, got %s", cfg.TenantID)
	}
}
