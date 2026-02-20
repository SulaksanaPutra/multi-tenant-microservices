package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"auth-service/internal/infrastructure/postgres"
	"auth-service/internal/testutil"
	"auth-service/internal/txcontext"
)

func TestCredentialRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewCredentialRepository(client)
	if repo == nil {
		t.Fatal("expected NewCredentialRepository to return non-nil struct pointer")
	}
}

func TestCredentialRepository_UpsertCredential(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var capturedQuery string
		var capturedArgs []any

		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				capturedQuery = query
				capturedArgs = args
				return testutil.MockResult{RowsAffectedVal: 1}, nil
			},
		}

		repo := NewCredentialRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		input := UpsertCredentialInput{
			UserID:       "usr_123",
			TenantID:     "tnt_456",
			Email:        "user@example.com",
			PasswordHash: "hashed_pwd",
		}

		err := repo.UpsertCredential(ctxWithExec, input)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		if !strings.Contains(capturedQuery, "INSERT INTO public.user_credentials") {
			t.Errorf("expected query to contain 'INSERT INTO public.user_credentials', got %s", capturedQuery)
		}
		if len(capturedArgs) != 4 || capturedArgs[0] != input.UserID || capturedArgs[2] != input.Email {
			t.Errorf("unexpected captured args: %v", capturedArgs)
		}
	})

	t.Run("db error", func(t *testing.T) {
		dbErr := errors.New("db error")
		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return nil, dbErr
			},
		}

		repo := NewCredentialRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		input := UpsertCredentialInput{Email: "error@example.com"}
		err := repo.UpsertCredential(ctxWithExec, input)
		if err == nil || !errors.Is(err, dbErr) {
			t.Errorf("expected wrapped db error, got %v", err)
		}
	})
}

func TestCredentialRepository_FindByEmail(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			capturedArgs = args
			return testutil.GetDummyRow(ctx)
		},
	}

	repo := NewCredentialRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	cred, err := repo.FindByEmail(ctxWithExec, "test@example.com")
	if cred != nil {
		t.Errorf("expected nil cred on scan error, got %+v", cred)
	}
	if err == nil {
		t.Fatal("expected scan error on dummy row, got nil")
	}

	if !strings.Contains(capturedQuery, "SELECT user_id, tenant_id, email, password_hash") ||
		!strings.Contains(capturedQuery, "FROM public.user_credentials") ||
		!strings.Contains(capturedQuery, "WHERE email = $1") {
		t.Errorf("unexpected query string: %s", capturedQuery)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "test@example.com" {
		t.Errorf("unexpected query args: %v", capturedArgs)
	}
}

func TestCredentialRepository_FindByUserID(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			capturedArgs = args
			return testutil.GetDummyRow(ctx)
		},
	}

	repo := NewCredentialRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	cred, err := repo.FindByUserID(ctxWithExec, "usr_123")
	if cred != nil {
		t.Errorf("expected nil cred on scan error, got %+v", cred)
	}
	if err == nil {
		t.Fatal("expected scan error on dummy row, got nil")
	}

	if !strings.Contains(capturedQuery, "WHERE user_id = $1") {
		t.Errorf("unexpected query string: %s", capturedQuery)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "usr_123" {
		t.Errorf("unexpected query args: %v", capturedArgs)
	}
}
