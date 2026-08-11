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
	PriorityChain   []domain.ProviderType
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials
}

func (r *PSPConfigRepository) SaveConfig(ctx context.Context, input SaveConfigInput, masterKey []byte) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)

	chainJSON, err := json.Marshal(input.PriorityChain)
	if err != nil {
		return fmt.Errorf("failed to marshal priority chain: %w", err)
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
		INSERT INTO payment_tenant_configs (tenant_id, priority_chain, encrypted_credentials, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id) DO UPDATE SET
			priority_chain = EXCLUDED.priority_chain,
			encrypted_credentials = EXCLUDED.encrypted_credentials,
			updated_at = EXCLUDED.updated_at
	`

	_, err = exec.ExecContext(ctx, query, input.TenantID, chainJSON, encryptedCreds, now, now)
	if err != nil {
		return fmt.Errorf("failed to save tenant PSP config: %w", err)
	}

	return nil
}

func (r *PSPConfigRepository) GetConfig(ctx context.Context, tenantID string, masterKey []byte) (*domain.TenantPSPConfig, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)

	query := `
		SELECT tenant_id, priority_chain, encrypted_credentials
		FROM payment_tenant_configs
		WHERE tenant_id = $1
	`

	var cfg domain.TenantPSPConfig
	var chainBytes, encryptedCreds []byte

	err := exec.QueryRowContext(ctx, query, tenantID).Scan(&cfg.TenantID, &chainBytes, &encryptedCreds)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // Return nil on not found
		}
		return nil, fmt.Errorf("failed to query tenant PSP config: %w", err)
	}

	if len(chainBytes) > 0 {
		_ = json.Unmarshal(chainBytes, &cfg.PriorityChain)
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
