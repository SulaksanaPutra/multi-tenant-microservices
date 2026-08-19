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
	outboxRepository := NewOutboxRepository(client)
	if outboxRepository == nil || outboxRepository.dbClient != client {
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

	outboxRepository := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := outboxRepository.SaveOutboxEvent(ctx, "evt-123", "payment.created", map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "INSERT INTO payment_outbox") {
		t.Errorf("expected query to contain 'INSERT INTO payment_outbox', got: %s", capturedQuery)
	}

	if len(capturedArgs) != 3 {
		t.Fatalf("expected 3 query args, got %d", len(capturedArgs))
	}
}

func TestPaymentOutboxRepository_SaveOutboxEvent_Error(t *testing.T) {
	dbErr := errors.New("db insert fail")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	outboxRepository := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := outboxRepository.SaveOutboxEvent(ctx, "evt-err", "payment.failed", "payload")
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

	outboxRepository := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := outboxRepository.MarkPublished(ctx, "evt-pub-100")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "UPDATE payment_outbox SET") {
		t.Errorf("expected update query, got %s", capturedQuery)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "evt-pub-100" {
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

	outboxRepository := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := outboxRepository.MarkFailed(ctx, "evt-fail-100", "connection refused")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "retry_count") || !strings.Contains(capturedQuery, "next_retry_at") {
		t.Errorf("expected update query with retry_count and next_retry_at, got %s", capturedQuery)
	}
	if len(capturedArgs) != 3 || capturedArgs[0] != "connection refused" || capturedArgs[2] != "evt-fail-100" {
		t.Errorf("unexpected captured args: %v", capturedArgs)
	}
}

func TestPaymentOutboxRepository_ListPending_QueryCheck(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			capturedQuery = query
			capturedArgs = args
			return nil, errors.New("query executed")
		},
	}

	outboxRepository := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, _ = outboxRepository.ListPending(ctx, 25)

	if !strings.Contains(capturedQuery, "FOR UPDATE SKIP LOCKED") {
		t.Errorf("expected query to contain 'FOR UPDATE SKIP LOCKED', got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "WITH claimed AS") {
		t.Errorf("expected query to contain CTE 'WITH claimed AS', got: %s", capturedQuery)
	}
	if len(capturedArgs) != 2 || capturedArgs[1] != 25 {
		t.Errorf("expected limit arg 25 as second param, got: %v", capturedArgs)
	}
}

func TestPaymentOutboxRepository_RecoverStuckClaims(t *testing.T) {
	var capturedQuery string
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			return testutil.MockResult{RowsAffectedVal: 2}, nil
		},
	}

	outboxRepository := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := outboxRepository.RecoverStuckClaims(ctx)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !strings.Contains(capturedQuery, "claimed_at < NOW() - $1::interval") {
		t.Errorf("expected query to recover stuck claims, got: %s", capturedQuery)
	}
}
