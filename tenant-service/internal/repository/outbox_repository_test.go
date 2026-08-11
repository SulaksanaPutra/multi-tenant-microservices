package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/testutil"
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
	client := &postgres.Client{}
	repo := NewOutboxRepository(client)
	if repo == nil {
		t.Fatal("expected NewOutboxRepository to return a non-nil struct pointer")
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

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateOutboxMessageInput{
		ID:            "msg-123",
		TenantID:      "tenant-999",
		AggregateType: "Tenant",
		AggregateID:   "tenant-999",
		EventType:     "workspace.initiated",
		Payload:       []byte(`{"name":"Acme Corp"}`),
	}

	err := repo.CreateOutboxMessage(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "INSERT INTO public.outbox") {
		t.Errorf("expected query to contain 'INSERT INTO public.outbox', got: %s", capturedQuery)
	}

	if len(capturedArgs) != 6 {
		t.Fatalf("expected 6 arguments, got %d", len(capturedArgs))
	}
	if capturedArgs[0] != input.ID || capturedArgs[2] != input.AggregateType || capturedArgs[4] != input.EventType {
		t.Errorf("unexpected arguments captured: %v", capturedArgs)
	}
}

func TestOutboxRepository_CreateOutboxMessage_Error(t *testing.T) {
	dbErr := errors.New("db insert fail")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateOutboxMessageInput{ID: "msg-err"}
	err := repo.CreateOutboxMessage(ctx, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to insert outbox message") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestOutboxRepository_FetchAndClaimBatch_QueryError(t *testing.T) {
	dbErr := errors.New("query error")
	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, dbErr
		},
	}
	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := repo.FetchAndClaimBatch(ctx, "workspace.initiated", 10)
	if err == nil {
		t.Fatal("expected error when QueryContext returns nil rows/error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to fetch and claim outbox batch") {
		t.Errorf("expected wrapped query error message, got: %v", err)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestOutboxRepository_FetchAndClaimBatch_EmptyResult(t *testing.T) {
	mockDB, _ := sql.Open("postgres", "host=localhost port=1 user=dummy dbname=dummy sslmode=disable")
	_ = mockDB.Close()

	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			rows, _ := mockDB.QueryContext(ctx, "SELECT 1 WHERE 1=0")
			return rows, nil
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	list, err := repo.FetchAndClaimBatch(ctx, "workspace.initiated", 10)
	if err != nil {
		t.Fatalf("expected nil error on empty batch result, got %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 messages, got %d", len(list))
	}
}

func TestOutboxRepository_RecoverStuckClaims_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return nil, nil
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.RecoverStuckClaims(ctx, "workspace.initiated")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "UPDATE public.outbox") || !strings.Contains(capturedQuery, "status     = 'PENDING'") {
		t.Errorf("expected recovery query, got %s", capturedQuery)
	}

	if len(capturedArgs) != 2 || capturedArgs[0] != "workspace.initiated" {
		t.Errorf("unexpected arguments: %v", capturedArgs)
	}
}

func TestOutboxRepository_RecoverStuckClaims_Error(t *testing.T) {
	dbErr := errors.New("recover error")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.RecoverStuckClaims(ctx, "workspace.initiated")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestOutboxRepository_MarkPublished_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return nil, nil
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.MarkPublished(ctx, "msg-777")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "status       = 'PUBLISHED'") {
		t.Errorf("expected PUBLISHED update query, got %s", capturedQuery)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "msg-777" {
		t.Errorf("unexpected captured args: %v", capturedArgs)
	}
}

func TestOutboxRepository_MarkPublished_Error(t *testing.T) {
	dbErr := errors.New("publish mark fail")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.MarkPublished(ctx, "msg-777")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected wrapped error to contain dbErr, got %v", err)
	}
}

func TestOutboxRepository_MarkFailed_Success(t *testing.T) {
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedArgs = args
			return nil, nil
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	inputErr := errors.New("network failure with password=secret")
	err := repo.MarkFailed(ctx, "msg-888", inputErr)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(capturedArgs) != 3 {
		t.Fatalf("expected 3 captured args (id, safeErr, maxRetries), got %d", len(capturedArgs))
	}

	if capturedArgs[0] != "msg-888" {
		t.Errorf("expected arg 0 to be msg-888, got %v", capturedArgs[0])
	}

	safeErrStr, ok := capturedArgs[1].(string)
	if !ok || strings.Contains(safeErrStr, "secret") {
		t.Errorf("expected sanitized error string without 'secret', got %v", capturedArgs[1])
	}
}

func TestOutboxRepository_MarkFailed_Error(t *testing.T) {
	dbErr := errors.New("fail mark fail")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	repo := NewOutboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.MarkFailed(ctx, "msg-888", errors.New("some error"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}
