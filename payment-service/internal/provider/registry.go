package provider

import (
	"context"
	"fmt"
	"sync"
	"time"

	"payment-service/internal/domain"
)

type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[domain.ProviderType]domain.PaymentProvider
	breakers  map[domain.ProviderType]*CircuitBreaker
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[domain.ProviderType]domain.PaymentProvider),
		breakers:  make(map[domain.ProviderType]*CircuitBreaker),
	}
}

func (providerRegistry *ProviderRegistry) RegisterProvider(paymentProvider domain.PaymentProvider, maxFailures int, cooldown time.Duration) {
	providerRegistry.mu.Lock()
	defer providerRegistry.mu.Unlock()

	id := paymentProvider.ID()
	providerRegistry.providers[id] = paymentProvider
	providerRegistry.breakers[id] = NewCircuitBreaker(maxFailures, cooldown)
}

func (providerRegistry *ProviderRegistry) GetProvider(id domain.ProviderType) (domain.PaymentProvider, bool) {
	providerRegistry.mu.RLock()
	defer providerRegistry.mu.RUnlock()

	paymentProvider, ok := providerRegistry.providers[id]
	return paymentProvider, ok
}

func (providerRegistry *ProviderRegistry) IsHealthy(id domain.ProviderType) bool {
	providerRegistry.mu.RLock()
	defer providerRegistry.mu.RUnlock()

	if _, ok := providerRegistry.providers[id]; !ok {
		return false
	}
	breaker := providerRegistry.breakers[id]
	return breaker == nil || breaker.Allow()
}

type FallbackExecutionOutput struct {
	Provider       domain.ProviderType
	PaymentMethod  string
	Session        *domain.PaymentSessionOutput
	FailedAttempts []domain.ProviderType
	AttemptErrors  map[domain.ProviderType]error
}

func (providerRegistry *ProviderRegistry) CreatePaymentSessionWithFallback(ctx context.Context, cfg *domain.TenantPSPConfig, req domain.CreateSessionRequest, methodID string) (*FallbackExecutionOutput, error) {
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
		paymentProvider, ok := providerRegistry.GetProvider(providerID)
		if !ok {
			res.FailedAttempts = append(res.FailedAttempts, providerID)
			res.AttemptErrors[providerID] = fmt.Errorf("provider %s not registered", providerID)
			continue
		}

		providerRegistry.mu.RLock()
		breaker := providerRegistry.breakers[providerID]
		providerRegistry.mu.RUnlock()

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

		session, err := paymentProvider.CreatePaymentSession(ctx, reqWithCreds)
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
