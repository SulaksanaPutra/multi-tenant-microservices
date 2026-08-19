package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"payment-service/internal/domain"
	"payment-service/internal/infrastructure/postgres"
	"payment-service/internal/testutil"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

func TestInboxRepository_TryInsert_Success(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	inboxRepository := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateInboxMessageInput{
		EventID:   "evt-payment-1001",
		TenantID:  "tenant-pay-abc",
		EventType: "order.created",
		Payload:   []byte(`{"order_id":"ord-123"}`),
	}

	isDuplicate, err := inboxRepository.TryInsert(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if isDuplicate {
		t.Errorf("expected isDuplicate to be false for new event, got true")
	}

	if !strings.Contains(capturedQuery, "INSERT INTO payment_inbox") {
		t.Errorf("expected query to contain 'INSERT INTO payment_inbox', got %s", capturedQuery)
	}

	if len(capturedArgs) != 4 {
		t.Fatalf("expected 4 query args, got %d", len(capturedArgs))
	}
	if capturedArgs[0] != input.EventID || capturedArgs[1] != input.TenantID || capturedArgs[2] != input.EventType || capturedArgs[3] != string(input.Payload) {
		t.Errorf("unexpected query args: %v", capturedArgs)
	}
}

func TestInboxRepository_TryInsert_DuplicateOnConflict(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return testutil.MockResult{RowsAffectedVal: 0}, nil
		},
	}

	inboxRepository := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateInboxMessageInput{EventID: "evt-dup-payment-1002"}
	isDuplicate, err := inboxRepository.TryInsert(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error on ON CONFLICT DO NOTHING, got %v", err)
	}
	if !isDuplicate {
		t.Errorf("expected isDuplicate to be true for duplicate event, got false")
	}
}

func TestInboxRepository_TryInsert_RowsAffectedError(t *testing.T) {
	raErr := errors.New("rows affected failed")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return testutil.MockResult{RowsAffectedErrVal: raErr}, nil
		},
	}

	inboxRepository := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateInboxMessageInput{EventID: "evt-ra-err-1003"}
	isDuplicate, err := inboxRepository.TryInsert(ctx, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if isDuplicate {
		t.Errorf("expected isDuplicate to be false on error, got true")
	}
	if !errors.Is(err, raErr) {
		t.Errorf("expected underlying error to be raErr, got %v", err)
	}
}

func TestInboxRepository_TryInsert_GenericError(t *testing.T) {
	dbErr := errors.New("connection failed")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	inboxRepository := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateInboxMessageInput{EventID: "evt-db-err-1004"}
	isDuplicate, err := inboxRepository.TryInsert(ctx, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if isDuplicate {
		t.Errorf("expected isDuplicate to be false on generic database error, got true")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestInboxRepository_SaveInboxEvent_SuccessAndDuplicate(t *testing.T) {
	mockExecSuccess := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}
	inboxRepository := NewInboxRepository(&postgres.Client{})
	ctxSuccess := txcontext.WithExecutor(context.Background(), mockExecSuccess)
	if err := inboxRepository.SaveInboxEvent(ctxSuccess, "evt-save-1", "order.created"); err != nil {
		t.Fatalf("expected nil error on SaveInboxEvent, got %v", err)
	}

	mockExecDup := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return testutil.MockResult{RowsAffectedVal: 0}, nil
		},
	}
	ctxDup := txcontext.WithExecutor(context.Background(), mockExecDup)
	err := inboxRepository.SaveInboxEvent(ctxDup, "evt-save-1", "order.created")
	if !errors.Is(err, domain.ErrDuplicateEvent) {
		t.Fatalf("expected domain.ErrDuplicateEvent, got %v", err)
	}
}
