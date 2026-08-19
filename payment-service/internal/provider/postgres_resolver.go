package provider

import (
	"context"
	"sync"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/repository"
)

type cachedPSPConfig struct {
	config   *domain.TenantPSPConfig
	cachedAt time.Time
}

type PostgresTenantPSPResolver struct {
	mu                  sync.RWMutex
	pspConfigRepository *repository.PSPConfigRepository
	masterKey           []byte
	inMemoryCache       map[string]cachedPSPConfig
	cacheTTL            time.Duration
}

func NewPostgresTenantPSPResolver(
	pspConfigRepository *repository.PSPConfigRepository,
	masterKey []byte,
) *PostgresTenantPSPResolver {
	return &PostgresTenantPSPResolver{
		pspConfigRepository: pspConfigRepository,
		masterKey:           masterKey,
		inMemoryCache:       make(map[string]cachedPSPConfig),
		cacheTTL:            30 * time.Second,
	}
}

func (r *PostgresTenantPSPResolver) ResolveConfig(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error) {
	r.mu.RLock()
	cached, ok := r.inMemoryCache[tenantID]
	r.mu.RUnlock()

	if ok && cached.config != nil && time.Since(cached.cachedAt) < r.cacheTTL {
		return cached.config, nil
	}

	if r.pspConfigRepository != nil {
		cfg, err := r.pspConfigRepository.GetConfig(ctx, tenantID, r.masterKey)
		if err == nil && cfg != nil {
			if cfg.Methods == nil {
				cfg.Methods = []domain.PaymentMethodConfig{}
			}
			if cfg.ProviderConfigs == nil {
				cfg.ProviderConfigs = make(map[domain.ProviderType]domain.ProviderCredentials)
			}
			r.mu.Lock()
			r.inMemoryCache[tenantID] = cachedPSPConfig{
				config:   cfg,
				cachedAt: time.Now(),
			}
			r.mu.Unlock()
			return cfg, nil
		}
	}

	emptyCfg := &domain.TenantPSPConfig{
		TenantID:        tenantID,
		Methods:         []domain.PaymentMethodConfig{},
		ProviderConfigs: make(map[domain.ProviderType]domain.ProviderCredentials),
	}

	r.mu.Lock()
	r.inMemoryCache[tenantID] = cachedPSPConfig{
		config:   emptyCfg,
		cachedAt: time.Now(),
	}
	r.mu.Unlock()

	return emptyCfg, nil
}

func (r *PostgresTenantPSPResolver) InvalidateCache(tenantID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if tenantID == "" {
		r.inMemoryCache = make(map[string]cachedPSPConfig)
	} else {
		delete(r.inMemoryCache, tenantID)
	}
}
