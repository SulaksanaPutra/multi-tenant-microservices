package service

import (
	"context"
	"errors"
	"log/slog"

	"payment-service/internal/domain"
	"payment-service/internal/repository"
)

type PSPConfigRepository interface {
	SaveConfig(ctx context.Context, input repository.SaveConfigInput) error
	FindByTenantID(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error)
	InvalidateCache(tenantID string)
}

type PSPConfigService struct {
	txManager           TxManager
	pspConfigRepository PSPConfigRepository
	masterKey           []byte
	logger              *slog.Logger
}

func NewPSPConfigService(
	txManager TxManager,
	pspConfigRepository PSPConfigRepository,
	masterKey []byte,
	logger *slog.Logger,
) *PSPConfigService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PSPConfigService{
		txManager:           txManager,
		pspConfigRepository: pspConfigRepository,
		masterKey:           masterKey,
		logger:              logger,
	}
}

type SavePSPConfigInput struct {
	TenantID        string
	Methods         []domain.PaymentMethodConfig
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials
}

type TenantPSPConfigOutput struct {
	TenantID        string
	Methods         []domain.PaymentMethodConfig
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials
}

func (pspConfigService *PSPConfigService) SaveConfig(ctx context.Context, input SavePSPConfigInput) error {
	if input.TenantID == "" {
		return errors.New("tenant_id is required")
	}

	saveFn := func(txCtx context.Context) error {
		return pspConfigService.pspConfigRepository.SaveConfig(txCtx, repository.SaveConfigInput{
			TenantID:        input.TenantID,
			Methods:         input.Methods,
			ProviderConfigs: input.ProviderConfigs,
		})
	}

	var err error
	if pspConfigService.txManager != nil {
		err = pspConfigService.txManager.WithTransaction(ctx, saveFn)
	} else {
		err = saveFn(ctx)
	}

	if err != nil {
		return err
	}

	// Evict stale cache entry so the next read fetches fresh data from the DB.
	pspConfigService.pspConfigRepository.InvalidateCache(input.TenantID)

	return nil
}

func (pspConfigService *PSPConfigService) GetConfig(ctx context.Context, tenantID string) (*TenantPSPConfigOutput, error) {
	if tenantID == "" {
		return nil, errors.New("tenant_id is required")
	}

	cfg, err := pspConfigService.pspConfigRepository.FindByTenantID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return toPSPConfigOutput(cfg), nil
}

func toPSPConfigOutput(cfg *domain.TenantPSPConfig) *TenantPSPConfigOutput {
	if cfg == nil {
		return nil
	}
	return &TenantPSPConfigOutput{
		TenantID:        cfg.TenantID,
		Methods:         cfg.Methods,
		ProviderConfigs: cfg.ProviderConfigs,
	}
}
