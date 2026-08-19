package service

import (
	"context"
	"errors"
	"log/slog"

	"payment-service/internal/domain"
	"payment-service/internal/repository"
)

type PSPConfigRepository interface {
	SaveConfig(ctx context.Context, input repository.SaveConfigInput, masterKey []byte) error
	GetConfig(ctx context.Context, tenantID string, masterKey []byte) (*domain.TenantPSPConfig, error)
}

type TenantPSPResolver interface {
	ResolveConfig(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error)
	InvalidateCache(tenantID string)
}

type PSPConfigService struct {
	txManager           TxManager
	pspConfigRepository PSPConfigRepository
	postgresResolver    TenantPSPResolver
	masterKey           []byte
	logger              *slog.Logger
}

func NewPSPConfigService(
	txManager TxManager,
	pspConfigRepository PSPConfigRepository,
	postgresResolver TenantPSPResolver,
	masterKey []byte,
	logger *slog.Logger,
) *PSPConfigService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PSPConfigService{
		txManager:           txManager,
		pspConfigRepository: pspConfigRepository,
		postgresResolver:    postgresResolver,
		masterKey:           masterKey,
		logger:              logger,
	}
}

type SavePSPConfigInput struct {
	TenantID        string
	PriorityChain   []domain.ProviderType
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials
}

type TenantPSPConfigOutput struct {
	TenantID        string
	PriorityChain   []domain.ProviderType
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials
}

func (pspConfigService *PSPConfigService) SaveConfig(ctx context.Context, input SavePSPConfigInput) error {
	if input.TenantID == "" {
		return errors.New("tenant_id is required")
	}

	saveFn := func(txCtx context.Context) error {
		return pspConfigService.pspConfigRepository.SaveConfig(txCtx, repository.SaveConfigInput{
			TenantID:        input.TenantID,
			PriorityChain:   input.PriorityChain,
			ProviderConfigs: input.ProviderConfigs,
		}, pspConfigService.masterKey)
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

	if pspConfigService.postgresResolver != nil {
		pspConfigService.postgresResolver.InvalidateCache(input.TenantID)
	}

	return nil
}

func (pspConfigService *PSPConfigService) GetConfig(ctx context.Context, tenantID string) (*TenantPSPConfigOutput, error) {
	if tenantID == "" {
		return nil, errors.New("tenant_id is required")
	}

	if pspConfigService.postgresResolver != nil {
		cfg, err := pspConfigService.postgresResolver.ResolveConfig(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		return toPSPConfigOutput(cfg), nil
	}

	cfg, err := pspConfigService.pspConfigRepository.GetConfig(ctx, tenantID, pspConfigService.masterKey)
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
		PriorityChain:   cfg.PriorityChain,
		ProviderConfigs: cfg.ProviderConfigs,
	}
}
