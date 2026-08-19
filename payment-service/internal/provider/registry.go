package provider

import (
	"context"
	"fmt"
	"sync"
	"time"

	"payment-service/internal/domain"
)

type TenantPSPResolver interface {
	ResolveConfig(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error)
}

type DefaultTenantPSPResolver struct {
	mu             sync.RWMutex
	tenantConfigs  map[string]*domain.TenantPSPConfig
	defaultMethods []domain.PaymentMethodConfig
}

func NewDefaultTenantPSPResolver(defaultMethods []domain.PaymentMethodConfig) *DefaultTenantPSPResolver {
	return &DefaultTenantPSPResolver{
		tenantConfigs:  make(map[string]*domain.TenantPSPConfig),
		defaultMethods: defaultMethods,
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
		Methods:         r.defaultMethods,
		ProviderConfigs: make(map[domain.ProviderType]domain.ProviderCredentials),
	}, nil
}

type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[domain.ProviderType]domain.PaymentProvider
	breakers  map[domain.ProviderType]*CircuitBreaker
	resolver  TenantPSPResolver
}

func NewProviderRegistry(resolver TenantPSPResolver) *ProviderRegistry {
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

type FallbackExecutionOutput struct {
	Provider       domain.ProviderType
	PaymentMethod  string
	Session        *domain.PaymentSessionOutput
	FailedAttempts []domain.ProviderType
	AttemptErrors  map[domain.ProviderType]error
}

func (r *ProviderRegistry) ListAvailableMethods(ctx context.Context, tenantID string) ([]domain.PaymentMethodConfig, error) {
	cfg, err := r.resolver.ResolveConfig(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve tenant PSP config: %w", err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var available []domain.PaymentMethodConfig
	for _, m := range cfg.Methods {
		if !m.Enabled {
			continue
		}

		hasHealthyProvider := false
		for _, providerID := range m.PriorityChain {
			if _, ok := r.providers[providerID]; !ok {
				continue
			}
			breaker := r.breakers[providerID]
			if breaker == nil || breaker.Allow() {
				hasHealthyProvider = true
				break
			}
		}

		if hasHealthyProvider {
			available = append(available, m)
		}
	}

	return available, nil
}

func (r *ProviderRegistry) ExecuteFallbackChain(ctx context.Context, req domain.CreateSessionRequest, methodID string) (*FallbackExecutionOutput, error) {
	cfg, err := r.resolver.ResolveConfig(ctx, req.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve tenant PSP config: %w", err)
	}

	var selectedMethod *domain.PaymentMethodConfig
	if methodID != "" {
		for i := range cfg.Methods {
			if cfg.Methods[i].ID == methodID && cfg.Methods[i].Enabled {
				selectedMethod = &cfg.Methods[i]
				break
			}
		}
		if selectedMethod == nil {
			return nil, domain.ErrInvalidPaymentMethod
		}
	} else {
		for i := range cfg.Methods {
			if cfg.Methods[i].Enabled {
				selectedMethod = &cfg.Methods[i]
				break
			}
		}
	}

	if selectedMethod == nil || len(selectedMethod.PriorityChain) == 0 {
		return nil, domain.ErrNoAvailableProvider
	}

	res := &FallbackExecutionOutput{
		PaymentMethod: selectedMethod.ID,
		AttemptErrors: make(map[domain.ProviderType]error),
	}

	for _, providerID := range selectedMethod.PriorityChain {
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

		reqWithCreds := req
		reqWithCreds.PaymentMethod = selectedMethod.ID
		if cfg.ProviderConfigs != nil {
			if creds, exists := cfg.ProviderConfigs[providerID]; exists {
				reqWithCreds.Credentials = creds
			}
		}

		session, err := p.CreatePaymentSession(ctx, reqWithCreds)
		if err != nil {
			if breaker != nil {
				breaker.OnFailure()
			}
			res.FailedAttempts = append(res.FailedAttempts, providerID)
			res.AttemptErrors[providerID] = err
			continue
		}

		if breaker != nil {
			breaker.OnSuccess()
		}

		res.Provider = providerID
		res.Session = session
		return res, nil
	}

	return res, domain.ErrNoAvailableProvider
}
