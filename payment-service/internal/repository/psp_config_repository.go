package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
	"payment-service/internal/crypto"
	"payment-service/internal/domain"
	"payment-service/internal/infrastructure/postgres"
)

type cachedPSPConfig struct {
	config   *domain.TenantPSPConfig
	cachedAt time.Time
}

type PSPConfigRepository struct {
	dbClient      *postgres.Client
	masterKey     []byte
	mu            sync.RWMutex
	inMemoryCache map[string]cachedPSPConfig
	cacheTTL      time.Duration
}

func NewPSPConfigRepository(dbClient *postgres.Client, masterKey []byte) *PSPConfigRepository {
	return &PSPConfigRepository{
		dbClient:      dbClient,
		masterKey:     masterKey,
		inMemoryCache: make(map[string]cachedPSPConfig),
		cacheTTL:      30 * time.Second,
	}
}

// InvalidateCache evicts the cached config for a specific tenant.
// Pass an empty string to invalidate all tenants.
func (pspConfigRepository *PSPConfigRepository) InvalidateCache(tenantID string) {
	pspConfigRepository.mu.Lock()
	defer pspConfigRepository.mu.Unlock()

	if tenantID == "" {
		pspConfigRepository.inMemoryCache = make(map[string]cachedPSPConfig)
	} else {
		delete(pspConfigRepository.inMemoryCache, tenantID)
	}
}

type SaveConfigInput struct {
	TenantID        string
	Methods         []domain.PaymentMethodConfig
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials
}

func (pspConfigRepository *PSPConfigRepository) SaveConfig(ctx context.Context, input SaveConfigInput) error {
	exec := txcontext.GetExecutor(ctx, pspConfigRepository.dbClient)

	methodsJSON, err := json.Marshal(input.Methods)
	if err != nil {
		return fmt.Errorf("failed to marshal payment methods: %w", err)
	}

	credsJSON, err := json.Marshal(input.ProviderConfigs)
	if err != nil {
		return fmt.Errorf("failed to marshal provider credentials: %w", err)
	}

	encryptedCreds, err := crypto.EncryptAESGCM(credsJSON, pspConfigRepository.masterKey)
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

// FindByTenantID fetches a tenant's PSP config, serving from the in-memory
// cache when a fresh entry exists, and falling back to PostgreSQL otherwise.
// Returns a zero-value config (not nil) when no record exists for the tenant.
func (pspConfigRepository *PSPConfigRepository) FindByTenantID(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error) {
	pspConfigRepository.mu.RLock()
	cached, ok := pspConfigRepository.inMemoryCache[tenantID]
	pspConfigRepository.mu.RUnlock()

	if ok && cached.config != nil && time.Since(cached.cachedAt) < pspConfigRepository.cacheTTL {
		return cached.config, nil
	}

	cfg, err := pspConfigRepository.fetchFromDB(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	pspConfigRepository.mu.Lock()
	pspConfigRepository.inMemoryCache[tenantID] = cachedPSPConfig{config: cfg, cachedAt: time.Now()}
	pspConfigRepository.mu.Unlock()

	return cfg, nil
}

func (pspConfigRepository *PSPConfigRepository) fetchFromDB(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error) {
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
			// No config row yet — return an empty but valid config so callers
			// don't need to nil-check; this is a valid "unconfigured" state.
			return &domain.TenantPSPConfig{
				TenantID:        tenantID,
				Methods:         []domain.PaymentMethodConfig{},
				ProviderConfigs: make(map[domain.ProviderType]domain.ProviderCredentials),
			}, nil
		}
		return nil, fmt.Errorf("failed to query tenant PSP config: %w", err)
	}

	if len(methodsBytes) > 0 {
		_ = json.Unmarshal(methodsBytes, &cfg.Methods)
	}

	if len(encryptedCreds) > 0 {
		decryptedJSON, err := crypto.DecryptAESGCM(encryptedCreds, pspConfigRepository.masterKey)
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
