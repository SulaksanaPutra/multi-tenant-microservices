package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"payment-service/internal/crypto"
	"payment-service/internal/domain"
	"payment-service/internal/infrastructure/postgres"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type PSPConfigRepository struct {
	dbClient *postgres.Client
}

func NewPSPConfigRepository(dbClient *postgres.Client) *PSPConfigRepository {
	return &PSPConfigRepository{dbClient: dbClient}
}

type SaveConfigInput struct {
	TenantID        string
	Methods         []domain.PaymentMethodConfig
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials
}

func (pspConfigRepository *PSPConfigRepository) SaveConfig(ctx context.Context, input SaveConfigInput, masterKey []byte) error {
	exec := txcontext.GetExecutor(ctx, pspConfigRepository.dbClient)

	methodsJSON, err := json.Marshal(input.Methods)
	if err != nil {
		return fmt.Errorf("failed to marshal payment methods: %w", err)
	}

	credsJSON, err := json.Marshal(input.ProviderConfigs)
	if err != nil {
		return fmt.Errorf("failed to marshal provider credentials: %w", err)
	}

	encryptedCreds, err := crypto.EncryptAESGCM(credsJSON, masterKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt provider credentials: %w", err)
	}

	now := time.Now()
	query := `
		INSERT INTO payment_tenant_configs (tenant_id, priority_chain, encrypted_credentials, methods, created_at, updated_at)
		VALUES ($1, '[]'::jsonb, $2, $3, $4, $5)
		ON CONFLICT (tenant_id) DO UPDATE SET
			encrypted_credentials = EXCLUDED.encrypted_credentials,
			methods = EXCLUDED.methods,
			updated_at = EXCLUDED.updated_at
	`

	_, err = exec.ExecContext(ctx, query, input.TenantID, encryptedCreds, methodsJSON, now, now)
	if err != nil {
		return fmt.Errorf("failed to save tenant PSP config: %w", err)
	}

	return nil
}

func (pspConfigRepository *PSPConfigRepository) GetConfig(ctx context.Context, tenantID string, masterKey []byte) (*domain.TenantPSPConfig, error) {
	exec := txcontext.GetExecutor(ctx, pspConfigRepository.dbClient)

	query := `
		SELECT tenant_id, encrypted_credentials, COALESCE(methods, '[]'::jsonb)
		FROM payment_tenant_configs
		WHERE tenant_id = $1
	`

	var cfg domain.TenantPSPConfig
	var encryptedCreds, methodsBytes []byte

	err := exec.QueryRowContext(ctx, query, tenantID).Scan(&cfg.TenantID, &encryptedCreds, &methodsBytes)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query tenant PSP config: %w", err)
	}

	if len(methodsBytes) > 0 {
		_ = json.Unmarshal(methodsBytes, &cfg.Methods)
	}

	if len(encryptedCreds) > 0 {
		decryptedJSON, err := crypto.DecryptAESGCM(encryptedCreds, masterKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt tenant PSP credentials: %w", err)
		}
		cfg.ProviderConfigs = make(map[domain.ProviderType]domain.ProviderCredentials)
		if err := json.Unmarshal(decryptedJSON, &cfg.ProviderConfigs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal decrypted credentials: %w", err)
		}
	}

	return &cfg, nil
}
