package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/testutil"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

func TestSanitizeError(t *testing.T) {
	tests := []struct {
		name     string
		inputErr error
		wantSub  string
		notWant  string
	}{
		{
			name:     "redact password",
			inputErr: errors.New("connection failed with password=supersecret123"),
			wantSub:  "[redacted]",
			notWant:  "supersecret123",
		},
		{
			name:     "redact amqp uri",
			inputErr: errors.New("dial failed: amqp://admin:secret@localhost:5672/vhost"),
			wantSub:  "[redacted]",
			notWant:  "admin:secret",
		},
		{
			name:     "redact postgres uri",
			inputErr: errors.New("db error: postgres://user:pass@localhost:5432/dbname"),
			wantSub:  "[redacted]",
			notWant:  "user:pass",
		},
		{
			name:     "truncate long message",
			inputErr: errors.New(strings.Repeat("a", 600)),
			wantSub:  "[truncated]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeError(tt.inputErr)
			if tt.wantSub != "" && !strings.Contains(got, tt.wantSub) {
				t.Errorf("expected sanitized error to contain %q, got: %s", tt.wantSub, got)
			}
			if tt.notWant != "" && strings.Contains(got, tt.notWant) {
				t.Errorf("expected sanitized error to NOT contain %q, got: %s", tt.notWant, got)
			}
		})
	}
}

func TestOutboxRepository_Constructor(t *testing.T) {
	cfg := tenantdb.Config{SchemaName: "tenant_100"}
	repo := NewOutboxRepository(cfg)
	if repo == nil {
		t.Fatal("expected NewOutboxRepository to return non-nil pointer")
	}
	if repo.schemaName() != "tenant_100" {
		t.Errorf("expected schemaName tenant_100, got %s", repo.schemaName())
	}

	defaultRepo := NewOutboxRepository(tenantdb.Config{})
	if defaultRepo.schemaName() != "public" {
		t.Errorf("expected default schemaName public, got %s", defaultRepo.schemaName())
	}
}

func TestOutboxRepository_CreateOutboxMessage_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return nil, nil
		},
	}

	repo := NewOutboxRepository(tenantdb.Config{SchemaName: "tenant_abc"})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateOutboxMessageInput{
		ID:            "msg-123",
		TenantID:      "tenant-abc",
		AggregateType: "Order",
		AggregateID:   "ord-123",
		EventType:     "order.created",
		Payload:       []byte(`{"total":100}`),
	}

	err := repo.CreateOutboxMessage(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, `"tenant_abc".outbox`) {
		t.Errorf("expected query to contain quoted schema outbox, got: %s", capturedQuery)
	}

	if len(capturedArgs) != 6 {
		t.Fatalf("expected 6 arguments, got %d", len(capturedArgs))
	}
}

func TestOutboxRepository_CreateOutboxMessage_Error(t *testing.T) {
	dbErr := errors.New("insert failure")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	repo := NewOutboxRepository(tenantdb.Config{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.CreateOutboxMessage(ctx, CreateOutboxMessageInput{ID: "msg-err"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected error to wrap dbErr, got %v", err)
	}
}

func TestOutboxRepository_FetchAndClaimBatch_QueryError(t *testing.T) {
	dbErr := errors.New("query failure")
	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, dbErr
		},
	}

	repo := NewOutboxRepository(tenantdb.Config{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := repo.FetchAndClaimBatch(ctx, "order.created", 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected error to wrap dbErr, got %v", err)
	}
}

func TestOutboxRepository_RecoverStuckClaims_Success(t *testing.T) {
	var capturedQuery string
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			return nil, nil
		},
	}

	repo := NewOutboxRepository(tenantdb.Config{SchemaName: "tenant_def"})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.RecoverStuckClaims(ctx, "order.created")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, `"tenant_def".outbox`) {
		t.Errorf("expected query to update tenant_def schema, got: %s", capturedQuery)
	}
}

func TestOutboxRepository_MarkPublished(t *testing.T) {
	var capturedArgs []any
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedArgs = args
			return nil, nil
		},
	}

	repo := NewOutboxRepository(tenantdb.Config{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.MarkPublished(ctx, "msg-777")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(capturedArgs) != 1 || capturedArgs[0] != "msg-777" {
		t.Errorf("unexpected captured args: %v", capturedArgs)
	}
}

func TestOutboxRepository_MarkFailed(t *testing.T) {
	var capturedArgs []any
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedArgs = args
			return nil, nil
		},
	}

	repo := NewOutboxRepository(tenantdb.Config{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.MarkFailed(ctx, "msg-888", errors.New("some error"))
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(capturedArgs) != 3 || capturedArgs[0] != "msg-888" {
		t.Errorf("unexpected captured args: %v", capturedArgs)
	}
}
