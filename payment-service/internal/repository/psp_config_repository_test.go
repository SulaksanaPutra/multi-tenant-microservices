package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"payment-service/internal/domain"
	"payment-service/internal/infrastructure/postgres"
	"payment-service/internal/testutil"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

func TestPSPConfigRepository_SaveAndFindByTenantID(t *testing.T) {
	client := &postgres.Client{}
	pspConfigRepository := NewPSPConfigRepository(client)
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

	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	masterKey := []byte("12345678901234567890123456789012")
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

	err := pspConfigRepository.SaveConfig(ctx, input, masterKey)
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
	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{})
	ctx := context.Background()

	input := SaveConfigInput{
		TenantID: "tenant-psp-2",
	}

	err := pspConfigRepository.SaveConfig(ctx, input, nil)
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

	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	masterKey := []byte("12345678901234567890123456789012")
	input := SaveConfigInput{TenantID: "tenant-psp-3"}

	err := pspConfigRepository.SaveConfig(ctx, input, masterKey)
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

	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	masterKey := []byte("12345678901234567890123456789012")
	_, err := pspConfigRepository.FindByTenantID(ctx, "tenant-err", masterKey)
	if err == nil {
		t.Fatal("expected error on row scan failure, got nil")
	}
}
