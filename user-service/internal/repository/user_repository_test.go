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

	if !strings.Contains(err.Error(), "failed to insert user record into public.users") {
		t.Errorf("expected error message to contain 'failed to insert user record into public.users', got: %v", err)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected wrapped error to be dbErr, got %v", err)
	}
}
