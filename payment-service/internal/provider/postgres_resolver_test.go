package provider

import (
	"context"
	"testing"
	"time"
)

func TestPostgresTenantPSPResolver_DefaultFallback(t *testing.T) {
	resolver := NewPostgresTenantPSPResolver(nil, []byte("master_key_1234"))

	cfg, err := resolver.ResolveConfig(context.Background(), "tenant_alpha")
	if err != nil {
		t.Fatalf("ResolveConfig failed: %v", err)
	}

	if cfg.TenantID != "tenant_alpha" {
		t.Fatalf("expected tenant_id 'tenant_alpha', got '%s'", cfg.TenantID)
	}

	if len(cfg.Methods) != 0 {
		t.Fatalf("expected 0 methods for unconfigured tenant, got: %d", len(cfg.Methods))
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

	// Test cache TTL expiry
	resolver.cacheTTL = 1 * time.Nanosecond
	cfg1, _ := resolver.ResolveConfig(context.Background(), "tenant_beta")
	time.Sleep(2 * time.Millisecond)
	cfg2, _ := resolver.ResolveConfig(context.Background(), "tenant_beta")
	if cfg1 == cfg2 {
		t.Fatalf("expected new pointer after cache TTL expiry")
	}
}
