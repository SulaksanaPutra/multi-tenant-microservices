package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/infrastructure/postgres"
	"payment-service/internal/testutil"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

var testMasterKey = []byte("12345678901234567890123456789012")

func TestPSPConfigRepository_SaveAndFindByTenantID(t *testing.T) {
	client := &postgres.Client{}
	pspConfigRepository := NewPSPConfigRepository(client, testMasterKey)
	if pspConfigRepository == nil || pspConfigRepository.dbClient != client {
		t.Fatal("expected NewPSPConfigRepository to return non-nil pointer with dbClient")
	}
}

func TestPSPConfigRepository_SaveConfig_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{}, testMasterKey)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := SaveConfigInput{
		TenantID: "tenant-psp-1",
		Methods: []domain.PaymentMethodConfig{
			{
				ID:            "bca_va",
				Name:          "BCA VA",
				Type:          domain.InstructionVirtualAccount,
				Enabled:       true,
				PriorityChain: []domain.ProviderType{domain.ProviderStripe, domain.ProviderXendit},
			},
		},
		ProviderConfigs: map[domain.ProviderType]domain.ProviderCredentials{},
	}

	err := pspConfigRepository.SaveConfig(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "INSERT INTO payment_tenant_configs") {
		t.Errorf("expected upsert query, got %s", capturedQuery)
	}

	if len(capturedArgs) != 5 {
		t.Fatalf("expected 5 captured args, got %d", len(capturedArgs))
	}
	if capturedArgs[0] != "tenant-psp-1" {
		t.Errorf("expected tenant_id tenant-psp-1, got %v", capturedArgs[0])
	}
}

func TestPSPConfigRepository_SaveConfig_EmptyMasterKey(t *testing.T) {
	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{}, nil)
	ctx := context.Background()

	input := SaveConfigInput{TenantID: "tenant-psp-2"}

	err := pspConfigRepository.SaveConfig(ctx, input)
	if err == nil {
		t.Fatal("expected encryption error for empty master key, got nil")
	}
	if !errors.Is(err, domain.ErrEmptyMasterKey) {
		t.Errorf("expected ErrEmptyMasterKey, got %v", err)
	}
}

func TestPSPConfigRepository_SaveConfig_DBError(t *testing.T) {
	dbErr := errors.New("db upsert failure")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{}, testMasterKey)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := pspConfigRepository.SaveConfig(ctx, SaveConfigInput{TenantID: "tenant-psp-3"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected error to wrap dbErr, got %v", err)
	}
}

func TestPSPConfigRepository_FindByTenantID_DBError(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			return testutil.GetDummyRow(ctx)
		},
	}

	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{}, testMasterKey)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := pspConfigRepository.FindByTenantID(ctx, "tenant-err")
	if err == nil {
		t.Fatal("expected error on row scan failure, got nil")
	}
}

func TestPSPConfigRepository_Cache_HitAndInvalidate(t *testing.T) {
	callCount := 0
	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			callCount++
			return testutil.GetDummyRow(ctx) // scan error → fetchFromDB returns error, no cache write
		},
	}

	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{}, testMasterKey)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	// Pre-seed the cache directly (same-package access) to simulate a prior successful fetch.
	seeded := &domain.TenantPSPConfig{TenantID: "tenant-cache-1", Methods: []domain.PaymentMethodConfig{}}
	pspConfigRepository.mu.Lock()
	pspConfigRepository.inMemoryCache["tenant-cache-1"] = cachedPSPConfig{config: seeded, cachedAt: time.Now()}
	pspConfigRepository.mu.Unlock()

	// Call with seeded cache — DB must NOT be called.
	got, err := pspConfigRepository.FindByTenantID(ctx, "tenant-cache-1")
	if err != nil {
		t.Fatalf("unexpected error on cache hit: %v", err)
	}
	if got != seeded {
		t.Fatal("expected cached pointer, got different value")
	}
	if callCount != 0 {
		t.Fatalf("expected 0 DB calls on cache hit, got %d", callCount)
	}

	// Invalidate → next call must attempt DB (will error, that's fine for this test).
	pspConfigRepository.InvalidateCache("tenant-cache-1")
	_, _ = pspConfigRepository.FindByTenantID(ctx, "tenant-cache-1")
	if callCount != 1 {
		t.Fatalf("expected 1 DB call after cache invalidation, got %d", callCount)
	}
}

func TestPSPConfigRepository_Cache_TTLExpiry(t *testing.T) {
	callCount := 0
	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			callCount++
			return testutil.GetDummyRow(ctx)
		},
	}

	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{}, testMasterKey)
	pspConfigRepository.cacheTTL = 1 * time.Nanosecond // expire immediately
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, _ = pspConfigRepository.FindByTenantID(ctx, "tenant-ttl")
	time.Sleep(2 * time.Millisecond) // let TTL expire
	_, _ = pspConfigRepository.FindByTenantID(ctx, "tenant-ttl")

	if callCount != 2 {
		t.Fatalf("expected 2 DB calls after TTL expiry, got %d", callCount)
	}
}

func TestPSPConfigRepository_InvalidateCache_All(t *testing.T) {
	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{}, testMasterKey)
	// Manually seed the cache
	pspConfigRepository.inMemoryCache["tnt_a"] = cachedPSPConfig{}
	pspConfigRepository.inMemoryCache["tnt_b"] = cachedPSPConfig{}

	pspConfigRepository.InvalidateCache("") // empty string = invalidate all
	if len(pspConfigRepository.inMemoryCache) != 0 {
		t.Fatalf("expected empty cache after full invalidation, got %d entries", len(pspConfigRepository.inMemoryCache))
	}
}
