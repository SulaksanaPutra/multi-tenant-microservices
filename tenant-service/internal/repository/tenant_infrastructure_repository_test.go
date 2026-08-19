package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/testutil"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

func TestTenantInfrastructureRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	tenantInfrastructureRepository := NewTenantInfrastructureRepository(client)
	if tenantInfrastructureRepository == nil {
		t.Fatal("expected NewTenantInfrastructureRepository to return a non-nil struct pointer")
	}
}

func TestTenantInfrastructureRepository_UpsertServiceInfrastructure_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	tenantInfrastructureRepository := NewTenantInfrastructureRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := UpsertServiceInfrastructureInput{
		TenantID:    "tenant-100",
		ServiceName: "order-service",
		DBHost:      "postgres-order",
		DBPort:      5433,
		DBName:      "order_db",
		DBUser:      "order_user",
		SchemaName:  "tenant_100",
	}

	err := tenantInfrastructureRepository.UpsertServiceInfrastructure(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "INSERT INTO public.tenant_infrastructures") ||
		!strings.Contains(capturedQuery, "ON CONFLICT (tenant_id, service_name) DO UPDATE") {
		t.Errorf("unexpected query string captured: %s", capturedQuery)
	}

	if len(capturedArgs) != 7 {
		t.Fatalf("expected 7 captured arguments, got %d", len(capturedArgs))
	}

	if capturedArgs[0] != "tenant-100" || capturedArgs[1] != "order-service" ||
		capturedArgs[2] != "postgres-order" || capturedArgs[3] != 5433 ||
		capturedArgs[4] != "order_db" || capturedArgs[5] != "order_user" ||
		capturedArgs[6] != "tenant_100" {
		t.Errorf("unexpected argument values captured: %v", capturedArgs)
	}
}

func TestTenantInfrastructureRepository_UpsertServiceInfrastructure_DefaultValues(t *testing.T) {
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	tenantInfrastructureRepository := NewTenantInfrastructureRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := UpsertServiceInfrastructureInput{
		TenantID:    "tenant-200",
		ServiceName: "user-service",
		DBHost:      "postgres-user",
		DBPort:      0, // Should default to 5432
		DBName:      "user_db",
		DBUser:      "", // Should default to "postgres"
		SchemaName:  "public",
	}

	err := tenantInfrastructureRepository.UpsertServiceInfrastructure(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(capturedArgs) != 7 {
		t.Fatalf("expected 7 captured arguments, got %d", len(capturedArgs))
	}

	if capturedArgs[3] != 5432 {
		t.Errorf("expected DBPort default to 5432, got %v", capturedArgs[3])
	}
	if capturedArgs[5] != "postgres" {
		t.Errorf("expected DBUser default to 'postgres', got %v", capturedArgs[5])
	}
}

func TestTenantInfrastructureRepository_UpsertServiceInfrastructure_ExecError(t *testing.T) {
	dbErr := errors.New("db connection failed")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	tenantInfrastructureRepository := NewTenantInfrastructureRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := UpsertServiceInfrastructureInput{
		TenantID:    "tenant-err",
		ServiceName: "order-service",
	}

	err := tenantInfrastructureRepository.UpsertServiceInfrastructure(ctx, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to upsert service infrastructure for tenant 'tenant-err'") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestTenantInfrastructureRepository_GetPendingServiceCount_EmptyRequiredServices(t *testing.T) {
	tenantInfrastructureRepository := NewTenantInfrastructureRepository(&postgres.Client{})

	count, err := tenantInfrastructureRepository.GetPendingServiceCount(context.Background(), "tenant-100", nil)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if count != 0 {
		t.Errorf("expected count 0 for empty required services, got %d", count)
	}

	count, err = tenantInfrastructureRepository.GetPendingServiceCount(context.Background(), "tenant-100", []string{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if count != 0 {
		t.Errorf("expected count 0 for empty slice, got %d", count)
	}
}

func TestTenantInfrastructureRepository_GetPendingServiceCount_SuccessAndQueryBuild(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			capturedArgs = args
			return testutil.GetDummyRow(ctx)
		},
	}

	tenantInfrastructureRepository := NewTenantInfrastructureRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	required := []string{"order-service", "user-service"}
	_, err := tenantInfrastructureRepository.GetPendingServiceCount(ctx, "tenant-100", required)
	// We expect Scan on dummy row to error, but capturedQuery and capturedArgs must be validated
	if err == nil {
		t.Fatal("expected error from scanning dummy row, got nil")
	}

	if !strings.Contains(capturedQuery, "VALUES ($2), ($3)") {
		t.Errorf("expected query to contain placeholders 'VALUES ($2), ($3)', got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "WHERE required.service_name NOT IN") {
		t.Errorf("expected query to contain WHERE clause, got: %s", capturedQuery)
	}

	if len(capturedArgs) != 3 {
		t.Fatalf("expected 3 captured arguments, got %d", len(capturedArgs))
	}
	if capturedArgs[0] != "tenant-100" || capturedArgs[1] != "order-service" || capturedArgs[2] != "user-service" {
		t.Errorf("unexpected captured arguments: %v", capturedArgs)
	}
}

func TestTenantInfrastructureRepository_GetPendingServiceCount_QueryError(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			return testutil.GetDummyRow(ctx)
		},
	}

	tenantInfrastructureRepository := NewTenantInfrastructureRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := tenantInfrastructureRepository.GetPendingServiceCount(ctx, "tenant-err", []string{"order-service"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to count pending services for tenant 'tenant-err'") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
}

func TestTenantInfrastructureRepository_GetServiceInfrastructure_Query(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			capturedArgs = args
			return testutil.GetDummyRow(ctx)
		},
	}

	tenantInfrastructureRepository := NewTenantInfrastructureRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := tenantInfrastructureRepository.GetServiceInfrastructure(ctx, "tenant-100", "order-service")
	if err == nil {
		t.Fatal("expected scan error on dummy row, got nil")
	}

	if !strings.Contains(capturedQuery, "SELECT db_host, db_port, db_name, db_user, COALESCE(schema_name, '')") ||
		!strings.Contains(capturedQuery, "WHERE tenant_id = $1 AND service_name = $2") {
		t.Errorf("unexpected query string captured: %s", capturedQuery)
	}

	if len(capturedArgs) != 2 || capturedArgs[0] != "tenant-100" || capturedArgs[1] != "order-service" {
		t.Errorf("unexpected captured arguments: %v", capturedArgs)
	}
	if !strings.Contains(err.Error(), "failed to query service infrastructure") {
		t.Errorf("expected wrapped query error message, got: %v", err)
	}
}
