package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"payment-service/internal/infrastructure/postgres"
	"payment-service/internal/testutil"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

func TestPaymentOutboxRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewOutboxRepository(client)
	if repo == nil || repo.dbClient != client {
		t.Fatal("expected NewOutboxRepository to return non-nil pointer with dbClient")
	}
}

func TestPaymentOutboxRepository_SaveOutboxEvent_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.SaveOutboxEvent(ctx, "evt-123", "payment.created", map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "INSERT INTO payment_outbox") {
		t.Errorf("expected query to contain 'INSERT INTO payment_outbox', got: %s", capturedQuery)
	}

	if len(capturedArgs) != 4 {
		t.Fatalf("expected 4 query args, got %d", len(capturedArgs))
	}
}

func TestPaymentOutboxRepository_SaveOutboxEvent_Error(t *testing.T) {
	dbErr := errors.New("db insert fail")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.SaveOutboxEvent(ctx, "evt-err", "payment.failed", "payload")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected error to wrap dbErr, got %v", err)
	}
}

func TestPaymentOutboxRepository_MarkPublished(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.MarkPublished(ctx, "evt-pub-100")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "UPDATE payment_outbox SET") {
		t.Errorf("expected update query, got %s", capturedQuery)
	}
	if len(capturedArgs) != 2 || capturedArgs[1] != "evt-pub-100" {
		t.Errorf("unexpected captured args: %v", capturedArgs)
	}
}

func TestPaymentOutboxRepository_MarkFailed(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.MarkFailed(ctx, "evt-fail-100", "connection refused")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "retry_count = retry_count + 1") {
		t.Errorf("expected update query, got %s", capturedQuery)
	}
	if len(capturedArgs) != 2 || capturedArgs[0] != "connection refused" || capturedArgs[1] != "evt-fail-100" {
		t.Errorf("unexpected captured args: %v", capturedArgs)
	}
}
