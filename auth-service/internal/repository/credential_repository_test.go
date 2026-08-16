package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"auth-service/internal/infrastructure/postgres"
	"auth-service/internal/testutil"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

func TestCredentialRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	credentialRepository := NewCredentialRepository(client)
	if credentialRepository == nil {
		t.Fatal("expected NewCredentialRepository to return non-nil struct pointer")
	}
}

func TestCredentialRepository_UpsertCredential(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var capturedQueries []string

		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				capturedQueries = append(capturedQueries, query)
				return testutil.MockResult{RowsAffectedVal: 1}, nil
			},
		}

		credentialRepository := NewCredentialRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		input := UpsertCredentialInput{
			UserID:       "usr_123",
			TenantID:     "tnt_456",
			Email:        "user@example.com",
			PasswordHash: "hashed_pwd",
		}

		err := credentialRepository.UpsertCredential(ctxWithExec, input)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		if len(capturedQueries) < 2 {
			t.Fatalf("expected at least 2 queries for credential + membership upsert, got %d", len(capturedQueries))
		}
		if !strings.Contains(capturedQueries[0], "INSERT INTO public.user_credentials") {
			t.Errorf("expected first query to contain 'INSERT INTO public.user_credentials', got %s", capturedQueries[0])
		}
		if !strings.Contains(capturedQueries[1], "INSERT INTO public.user_tenant_memberships") {
			t.Errorf("expected second query to contain 'INSERT INTO public.user_tenant_memberships', got %s", capturedQueries[1])
		}
	})

	t.Run("db error", func(t *testing.T) {
		dbErr := errors.New("db error")
		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return nil, dbErr
			},
		}

		credentialRepository := NewCredentialRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		input := UpsertCredentialInput{Email: "error@example.com"}
		err := credentialRepository.UpsertCredential(ctxWithExec, input)
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

	credentialRepository := NewCredentialRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	cred, err := credentialRepository.FindByEmail(ctxWithExec, "test@example.com")
	if cred != nil {
		t.Errorf("expected nil cred on scan error, got %+v", cred)
	}
	if err == nil {
		t.Fatal("expected scan error on dummy row, got nil")
	}

	if !strings.Contains(capturedQuery, "SELECT user_id, email, password_hash") ||
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

	credentialRepository := NewCredentialRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	cred, err := credentialRepository.FindByUserID(ctxWithExec, "usr_123")
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
