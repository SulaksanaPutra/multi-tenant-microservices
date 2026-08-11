package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure/postgres"
	"auth-service/internal/testutil"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

func TestSetupTokenRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewSetupTokenRepository(client)
	if repo == nil {
		t.Fatal("expected NewSetupTokenRepository to return non-nil struct pointer")
	}
}

func TestSetupTokenRepository_CreateSetupToken(t *testing.T) {
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

		repo := NewSetupTokenRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		input := CreateSetupTokenInput{
			UserID:    "usr_123",
			TenantID:  "tnt_456",
			Email:     "setup@example.com",
			TokenHash: "token_hash_abc",
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}

		err := repo.CreateSetupToken(ctxWithExec, input)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		if !strings.Contains(capturedQuery, "INSERT INTO public.password_setup_tokens") {
			t.Errorf("expected query to contain 'INSERT INTO public.password_setup_tokens', got %s", capturedQuery)
		}
		if len(capturedArgs) != 5 || capturedArgs[0] != input.UserID || capturedArgs[3] != input.TokenHash {
			t.Errorf("unexpected query arguments: %v", capturedArgs)
		}
	})

	t.Run("db error", func(t *testing.T) {
		dbErr := errors.New("db insert failure")
		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return nil, dbErr
			},
		}

		repo := NewSetupTokenRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		input := CreateSetupTokenInput{TokenHash: "hash_err"}
		err := repo.CreateSetupToken(ctxWithExec, input)
		if err == nil || !errors.Is(err, dbErr) {
			t.Errorf("expected wrapped db error, got %v", err)
		}
	})
}

func TestSetupTokenRepository_FindByTokenHash(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			capturedArgs = args
			return testutil.GetDummyRow(ctx)
		},
	}

	repo := NewSetupTokenRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	token, err := repo.FindByTokenHash(ctxWithExec, "hash_xyz")
	if token != nil {
		t.Errorf("expected nil token on dummy scan, got %+v", token)
	}
	if err == nil {
		t.Fatal("expected scan error on dummy row, got nil")
	}

	if !strings.Contains(capturedQuery, "FROM public.password_setup_tokens") ||
		!strings.Contains(capturedQuery, "WHERE token_hash = $1") {
		t.Errorf("unexpected query string: %s", capturedQuery)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "hash_xyz" {
		t.Errorf("unexpected query args: %v", capturedArgs)
	}
}

func TestSetupTokenRepository_MarkTokenUsed(t *testing.T) {
	t.Run("success rows=1", func(t *testing.T) {
		var capturedQuery string
		var capturedArgs []any

		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				capturedQuery = query
				capturedArgs = args
				return testutil.MockResult{RowsAffectedVal: 1}, nil
			},
		}

		repo := NewSetupTokenRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		err := repo.MarkTokenUsed(ctxWithExec, "valid_hash")
		if err != nil {
			t.Fatalf("expected nil error on first spend attempt, got %v", err)
		}

		if !strings.Contains(capturedQuery, "UPDATE public.password_setup_tokens") ||
			!strings.Contains(capturedQuery, "WHERE token_hash = $1 AND used_at IS NULL") {
			t.Errorf("unexpected query string: %s", capturedQuery)
		}
		if len(capturedArgs) != 1 || capturedArgs[0] != "valid_hash" {
			t.Errorf("unexpected query args: %v", capturedArgs)
		}
	})

	t.Run("double-spend attempt (rows=0) -> ErrTokenAlreadyUsed", func(t *testing.T) {
		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return testutil.MockResult{RowsAffectedVal: 0}, nil
			},
		}

		repo := NewSetupTokenRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		err := repo.MarkTokenUsed(ctxWithExec, "already_used_hash")
		if !errors.Is(err, domain.ErrTokenAlreadyUsed) {
			t.Fatalf("expected ErrTokenAlreadyUsed on double-spend attempt, got %v", err)
		}
	})

	t.Run("db error", func(t *testing.T) {
		dbErr := errors.New("update error")
		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return nil, dbErr
			},
		}

		repo := NewSetupTokenRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		err := repo.MarkTokenUsed(ctxWithExec, "some_hash")
		if err == nil || !errors.Is(err, dbErr) {
			t.Errorf("expected wrapped db error, got %v", err)
		}
	})
}
