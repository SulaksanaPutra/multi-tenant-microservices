package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"user-service/internal/infrastructure/postgres"
	"user-service/internal/testutil"
	"user-service/internal/txcontext"
)

func TestUserRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewUserRepository(client)
	if repo == nil {
		t.Fatal("expected NewUserRepository to return a non-nil struct pointer")
	}
}

func TestUserRepository_CreateUser_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return nil, nil
		},
	}

	repo := NewUserRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	userInput := CreateUserInput{
		ID:    "user-123",
		Email: "test@example.com",
		Name:  "Test User",
	}

	err := repo.CreateUser(ctx, userInput)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "INSERT INTO public.users") {
		t.Errorf("expected query to contain 'INSERT INTO public.users', got: %s", capturedQuery)
	}

	if len(capturedArgs) != 3 {
		t.Fatalf("expected 3 query arguments, got %d", len(capturedArgs))
	}

	if capturedArgs[0] != userInput.ID || capturedArgs[1] != userInput.Email || capturedArgs[2] != userInput.Name {
		t.Errorf("unexpected query arguments: got %v, expected [%s, %s, %s]",
			capturedArgs, userInput.ID, userInput.Email, userInput.Name)
	}
}

func TestUserRepository_CreateUser_Error(t *testing.T) {
	dbErr := errors.New("database connection error")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	repo := NewUserRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	userInput := CreateUserInput{
		ID:    "user-456",
		Email: "fail@example.com",
		Name:  "Fail User",
	}

	err := repo.CreateUser(ctx, userInput)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "user repository: failed to insert user record into public.users") {
		t.Errorf("expected error message to contain 'user repository: failed to insert user record into public.users', got: %v", err)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected wrapped error to be dbErr, got %v", err)
	}
}

func TestUserRepository_UpdateUser_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return &testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	userRepository := NewUserRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := userRepository.UpdateUser(ctx, UpdateUserInput{ID: "usr_123", Name: "Updated Name"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "UPDATE public.users") {
		t.Errorf("expected query to contain UPDATE, got %s", capturedQuery)
	}
	if len(capturedArgs) != 2 || capturedArgs[0] != "usr_123" || capturedArgs[1] != "Updated Name" {
		t.Errorf("unexpected query args: %v", capturedArgs)
	}
}

func TestUserRepository_UpdateUser_NotFound(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return &testutil.MockResult{RowsAffectedVal: 0}, nil
		},
	}

	userRepository := NewUserRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := userRepository.UpdateUser(ctx, UpdateUserInput{ID: "usr_missing", Name: "Name"})
	if err == nil {
		t.Fatal("expected error for non-existent user, got nil")
	}
}

func TestUserRepository_ListUsers_Error(t *testing.T) {
	dbErr := errors.New("db error")
	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, dbErr
		},
	}

	userRepository := NewUserRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := userRepository.ListUsers(ctx, "tenant-1")
	if err == nil || !errors.Is(err, dbErr) {
		t.Fatalf("expected error wrapping dbErr, got %v", err)
	}
}

func TestUserRepository_ListUsers_SuccessScopedToTenant(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			capturedQuery = query
			capturedArgs = args
			return nil, errors.New("stop after query capture")
		},
	}

	userRepository := NewUserRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := userRepository.ListUsers(ctx, "tenant-9")
	if err == nil {
		t.Fatal("expected error to short-circuit after query capture")
	}

	if !strings.Contains(capturedQuery, "user_tenant_memberships") {
		t.Errorf("expected query to join user_tenant_memberships, got: %s", capturedQuery)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "tenant-9" {
		t.Errorf("expected query scoped to tenant-9, got args %v", capturedArgs)
	}
}

func TestUserRepository_ListUsers_RequiresTenant(t *testing.T) {
	userRepository := NewUserRepository(&postgres.Client{})
	_, err := userRepository.ListUsers(context.Background(), "")
	if err == nil {
		t.Fatal("expected error when tenant_id is empty")
	}
}

func TestUserRepository_AddUserTenantMembership_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return nil, nil
		},
	}

	userRepository := NewUserRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	if err := userRepository.AddUserTenantMembership(ctx, "usr_1", "tenant-1"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !strings.Contains(capturedQuery, "user_tenant_memberships") {
		t.Errorf("expected query to insert user_tenant_memberships, got: %s", capturedQuery)
	}
	if len(capturedArgs) != 2 || capturedArgs[0] != "usr_1" || capturedArgs[1] != "tenant-1" {
		t.Errorf("unexpected membership args: %v", capturedArgs)
	}
}
