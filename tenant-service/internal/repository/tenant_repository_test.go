package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/testutil"
)

func TestTenantRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	tenantRepository := NewTenantRepository(client)
	if tenantRepository == nil {
		t.Fatal("expected NewTenantRepository to return a non-nil struct pointer")
	}
}

func TestTenantRepository_CreateTenant_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	tenantRepository := NewTenantRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateTenantInput{
		ID:         "t-100",
		Name:       "Acme Corp",
		Slug:       "acme",
		OwnerEmail: "owner@acme.com",
		OwnerName:  "Alice Smith",
		Plan:       "enterprise",
	}

	err := tenantRepository.CreateTenant(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "INSERT INTO public.tenants (id, name, slug, owner_email, owner_name, plan, status)") ||
		!strings.Contains(capturedQuery, "VALUES ($1, $2, $3, $4, $5, $6, 'pending')") {
		t.Errorf("unexpected query string captured: %s", capturedQuery)
	}

	if len(capturedArgs) != 6 {
		t.Fatalf("expected 6 captured arguments, got %d", len(capturedArgs))
	}

	if capturedArgs[0] != "t-100" || capturedArgs[1] != "Acme Corp" ||
		capturedArgs[2] != "acme" || capturedArgs[3] != "owner@acme.com" ||
		capturedArgs[4] != "Alice Smith" || capturedArgs[5] != "enterprise" {
		t.Errorf("unexpected captured argument values: %v", capturedArgs)
	}
}

func TestTenantRepository_CreateTenant_ExecError(t *testing.T) {
	dbErr := errors.New("unique constraint violation")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	tenantRepository := NewTenantRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateTenantInput{
		ID:   "t-err",
		Name: "Error Corp",
	}

	err := tenantRepository.CreateTenant(ctx, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to insert tenant record") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestTenantRepository_ActivateTenant_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	tenantRepository := NewTenantRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := tenantRepository.ActivateTenant(ctx, "t-100")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "UPDATE public.tenants SET status = 'active' WHERE id = $1") {
		t.Errorf("unexpected query string captured: %s", capturedQuery)
	}

	if len(capturedArgs) != 1 || capturedArgs[0] != "t-100" {
		t.Errorf("unexpected captured arguments: %v", capturedArgs)
	}
}

func TestTenantRepository_ActivateTenant_ExecError(t *testing.T) {
	dbErr := errors.New("update statement failure")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	tenantRepository := NewTenantRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := tenantRepository.ActivateTenant(ctx, "t-err")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to activate tenant 't-err'") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestTenantRepository_FindByID_Query(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			capturedArgs = args
			return testutil.GetDummyRow(ctx)
		},
	}

	tenantRepository := NewTenantRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := tenantRepository.FindByID(ctx, "t-100")
	if err == nil {
		t.Fatal("expected scan error on dummy row, got nil")
	}

	if !strings.Contains(capturedQuery, "SELECT id, name, slug, owner_email, owner_name, plan, status, created_at") ||
		!strings.Contains(capturedQuery, "FROM public.tenants") ||
		!strings.Contains(capturedQuery, "WHERE id = $1") {
		t.Errorf("unexpected query string captured: %s", capturedQuery)
	}

	if len(capturedArgs) != 1 || capturedArgs[0] != "t-100" {
		t.Errorf("unexpected captured arguments: %v", capturedArgs)
	}
	if !strings.Contains(err.Error(), "failed to query tenant by ID") {
		t.Errorf("expected wrapped query error message, got: %v", err)
	}
}

func TestTenantRepository_UpdateTenant_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	tenantRepository := NewTenantRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	ownerEmail := "owner@example.com"
	ownerName := "Owner Name"
	err := tenantRepository.UpdateTenant(ctx, UpdateTenantInput{
		ID:         "t-100",
		Name:       "Updated Name",
		Slug:       "updated-slug",
		OwnerEmail: &ownerEmail,
		OwnerName:  &ownerName,
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "UPDATE public.tenants") {
		t.Errorf("expected query to contain UPDATE, got %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "owner_email = $4") || !strings.Contains(capturedQuery, "owner_name = $5") {
		t.Errorf("expected query to include owner columns, got %s", capturedQuery)
	}
	if len(capturedArgs) != 5 || capturedArgs[0] != "t-100" {
		t.Errorf("unexpected captured args: %v", capturedArgs)
	}
}

func TestTenantRepository_UpdateTenant_OwnerFieldsOmitted(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	tenantRepository := NewTenantRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := tenantRepository.UpdateTenant(ctx, UpdateTenantInput{
		ID:   "t-100",
		Name: "Updated Name",
		Slug: "updated-slug",
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if strings.Contains(capturedQuery, "owner_email") || strings.Contains(capturedQuery, "owner_name") {
		t.Errorf("expected query NOT to update owner columns when omitted, got %s", capturedQuery)
	}
	if len(capturedArgs) != 3 || capturedArgs[0] != "t-100" {
		t.Errorf("unexpected captured args: %v", capturedArgs)
	}
}

func TestTenantRepository_UpdateTenantPlan_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	tenantRepository := NewTenantRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := tenantRepository.UpdateTenantPlan(ctx, UpdateTenantPlanInput{
		ID:   "t-100",
		Plan: "dedicated",
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "SET plan = $2") {
		t.Errorf("expected query to contain SET plan, got %s", capturedQuery)
	}
	if len(capturedArgs) != 2 || capturedArgs[0] != "t-100" || capturedArgs[1] != "dedicated" {
		t.Errorf("unexpected captured args: %v", capturedArgs)
	}
}
