package provider

import (
	"context"
	"testing"

	"payment-service/internal/domain"
)

func TestPostgresTenantPSPResolver_DefaultFallback(t *testing.T) {
	resolver := NewPostgresTenantPSPResolver(nil, []byte("master_key_1234"), []domain.ProviderType{
		domain.ProviderMock,
		domain.ProviderDirectBank,
	})

	cfg, err := resolver.ResolveConfig(context.Background(), "tenant_alpha")
	if err != nil {
		t.Fatalf("ResolveConfig failed: %v", err)
	}

	if cfg.TenantID != "tenant_alpha" {
		t.Fatalf("expected tenant_id 'tenant_alpha', got '%s'", cfg.TenantID)
	}

	if len(cfg.PriorityChain) != 2 || cfg.PriorityChain[0] != domain.ProviderMock {
		t.Fatalf("unexpected priority chain: %v", cfg.PriorityChain)
	}

	// Test in-memory cache hit
	cachedCfg, err := resolver.ResolveConfig(context.Background(), "tenant_alpha")
	if err != nil {
		t.Fatalf("ResolveConfig cache hit failed: %v", err)
	}
	if cachedCfg != cfg {
		t.Fatalf("expected same pointer from cache hit")
	}

	// Test cache invalidation
	resolver.InvalidateCache("tenant_alpha")
	if len(resolver.inMemoryCache) != 0 {
		t.Fatalf("expected empty cache after invalidation")
	}
}
