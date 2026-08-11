package provider

import (
	"context"
	"fmt"
	"sync"
	"time"

	"payment-service/internal/domain"
)

type DefaultTenantPSPResolver struct {
	mu           sync.RWMutex
	tenantConfigs map[string]*domain.TenantPSPConfig
	defaultChain  []domain.ProviderType
}

func NewDefaultTenantPSPResolver(defaultChain []domain.ProviderType) *DefaultTenantPSPResolver {
	if len(defaultChain) == 0 {
		defaultChain = []domain.ProviderType{
			domain.ProviderMock,
			domain.ProviderDirectBank,
		}
	}
	return &DefaultTenantPSPResolver{
		tenantConfigs: make(map[string]*domain.TenantPSPConfig),
		defaultChain:  defaultChain,
	}
}

func (r *DefaultTenantPSPResolver) SetTenantConfig(config *domain.TenantPSPConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tenantConfigs[config.TenantID] = config
}

func (r *DefaultTenantPSPResolver) ResolveConfig(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if cfg, ok := r.tenantConfigs[tenantID]; ok {
		return cfg, nil
	}

	return &domain.TenantPSPConfig{
		TenantID:        tenantID,
		PriorityChain:   r.defaultChain,
		ProviderConfigs: make(map[domain.ProviderType]domain.ProviderCredentials),
	}, nil
}

type ProviderRegistry struct {
	mu         sync.RWMutex
	providers  map[domain.ProviderType]domain.PaymentProvider
	breakers   map[domain.ProviderType]*CircuitBreaker
	resolver   domain.TenantPSPResolver
}

func NewProviderRegistry(resolver domain.TenantPSPResolver) *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[domain.ProviderType]domain.PaymentProvider),
		breakers:  make(map[domain.ProviderType]*CircuitBreaker),
		resolver:  resolver,
	}
}

func (r *ProviderRegistry) RegisterProvider(p domain.PaymentProvider, maxFailures int, cooldown time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := p.ID()
	r.providers[id] = p
	r.breakers[id] = NewCircuitBreaker(maxFailures, cooldown)
}

func (r *ProviderRegistry) GetProvider(id domain.ProviderType) (domain.PaymentProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[id]
	return p, ok
}

type ExecutionResult struct {
	Provider          domain.ProviderType
	Session           *domain.PaymentSessionResult
	FailedAttempts    []domain.ProviderType
	AttemptErrors     map[domain.ProviderType]error
}

func (r *ProviderRegistry) ExecuteFallbackChain(ctx context.Context, req domain.CreateSessionRequest) (*ExecutionResult, error) {
	cfg, err := r.resolver.ResolveConfig(ctx, req.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve tenant PSP config: %w", err)
	}

	res := &ExecutionResult{
		AttemptErrors: make(map[domain.ProviderType]error),
	}

	for _, providerID := range cfg.PriorityChain {
		p, ok := r.GetProvider(providerID)
		if !ok {
			res.FailedAttempts = append(res.FailedAttempts, providerID)
			res.AttemptErrors[providerID] = fmt.Errorf("provider %s not registered", providerID)
			continue
		}

		breaker := r.breakers[providerID]
		if breaker != nil && !breaker.Allow() {
			res.FailedAttempts = append(res.FailedAttempts, providerID)
			res.AttemptErrors[providerID] = domain.ErrCircuitOpen
			continue
		}

		session, err := p.CreatePaymentSession(ctx, req)
		if err != nil {
			if breaker != nil {
				breaker.RecordFailure()
			}
			res.FailedAttempts = append(res.FailedAttempts, providerID)
			res.AttemptErrors[providerID] = err
			continue
		}

		if breaker != nil {
			breaker.RecordSuccess()
		}

		res.Provider = providerID
		res.Session = session
		return res, nil
	}

	return res, domain.ErrNoAvailableProvider
}
