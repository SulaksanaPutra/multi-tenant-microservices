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
	"auth-service/internal/txcontext"
)

func TestTokenRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewTokenRepository(client)
	if repo == nil {
		t.Fatal("expected NewTokenRepository to return non-nil struct pointer")
	}
}

func TestTokenRepository_CreateRefreshToken(t *testing.T) {
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

		repo := NewTokenRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		input := CreateRefreshTokenInput{
			UserID:    "usr_123",
			TokenHash: "refresh_hash_abc",
			ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		}

		err := repo.CreateRefreshToken(ctxWithExec, input)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		if !strings.Contains(capturedQuery, "INSERT INTO public.refresh_tokens") {
			t.Errorf("expected query to contain 'INSERT INTO public.refresh_tokens', got %s", capturedQuery)
		}
		if len(capturedArgs) != 3 || capturedArgs[0] != input.UserID || capturedArgs[1] != input.TokenHash {
			t.Errorf("unexpected query arguments: %v", capturedArgs)
		}
	})

	t.Run("db error", func(t *testing.T) {
		dbErr := errors.New("insert refresh token db error")
		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return nil, dbErr
			},
		}

		repo := NewTokenRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		input := CreateRefreshTokenInput{TokenHash: "err_hash"}
		err := repo.CreateRefreshToken(ctxWithExec, input)
		if err == nil || !errors.Is(err, dbErr) {
			t.Errorf("expected wrapped db error, got %v", err)
		}
	})
}

func TestTokenRepository_FindByTokenHash(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			capturedArgs = args
			return testutil.GetDummyRow(ctx)
		},
	}

	repo := NewTokenRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	token, err := repo.FindByTokenHash(ctxWithExec, "hash_abc")
	if token != nil {
		t.Errorf("expected nil token on dummy scan, got %+v", token)
	}
	if err == nil {
		t.Fatal("expected scan error on dummy row, got nil")
	}

	if !strings.Contains(capturedQuery, "FROM public.refresh_tokens") ||
		!strings.Contains(capturedQuery, "WHERE token_hash = $1") {
		t.Errorf("unexpected query string: %s", capturedQuery)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "hash_abc" {
		t.Errorf("unexpected query args: %v", capturedArgs)
	}
}

func TestTokenRepository_RevokeRefreshToken(t *testing.T) {
	t.Run("success rows=1", func(t *testing.T) {
		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return testutil.MockResult{RowsAffectedVal: 1}, nil
			},
		}

		repo := NewTokenRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		err := repo.RevokeRefreshToken(ctxWithExec, RevokeRefreshTokenInput{TokenHash: "hash_1"})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("token not found or already revoked (rows=0) -> ErrTokenNotFound", func(t *testing.T) {
		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return testutil.MockResult{RowsAffectedVal: 0}, nil
			},
		}

		repo := NewTokenRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		err := repo.RevokeRefreshToken(ctxWithExec, RevokeRefreshTokenInput{TokenHash: "hash_0"})
		if !errors.Is(err, domain.ErrTokenNotFound) {
			t.Errorf("expected ErrTokenNotFound when rows affected = 0, got %v", err)
		}
	})
}

func TestTokenRepository_DeleteRefreshToken(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return testutil.MockResult{RowsAffectedVal: 1}, nil
			},
		}

		repo := NewTokenRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		err := repo.DeleteRefreshToken(ctxWithExec, DeleteRefreshTokenInput{TokenHash: "del_hash"})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})
}
