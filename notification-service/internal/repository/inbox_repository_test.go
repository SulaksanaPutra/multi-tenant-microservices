package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"notification-service/internal/infrastructure/postgres"
	"notification-service/internal/testutil"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

func TestInboxRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	inboxRepository := NewInboxRepository(client)
	if inboxRepository == nil {
		t.Fatal("expected NewInboxRepository to return a non-nil struct pointer")
	}
}

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
		EventID:   "evt-1001",
		TenantID:  "tenant-abc",
		EventType: "workspace.initiated",
		Payload:   nil,
	}

	isDuplicate, err := inboxRepository.TryInsert(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if isDuplicate {
		t.Errorf("expected isDuplicate to be false for new event, got true")
	}

	if !strings.Contains(capturedQuery, "INSERT INTO public.inbox") {
		t.Errorf("expected query to contain 'INSERT INTO public.inbox', got %s", capturedQuery)
	}

	if len(capturedArgs) != 4 {
		t.Fatalf("expected 4 query args, got %d", len(capturedArgs))
	}
	if capturedArgs[0] != input.EventID || capturedArgs[1] != input.TenantID || capturedArgs[2] != input.EventType || capturedArgs[3] != "{}" {
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

	input := CreateInboxMessageInput{EventID: "evt-dup-1002"}
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

	input := CreateInboxMessageInput{EventID: "evt-ra-1003"}
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

	input := CreateInboxMessageInput{EventID: "evt-err-1004"}
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

func TestInboxRepository_ListEventsByTenantID_QueryError(t *testing.T) {
	dbErr := errors.New("query failure")
	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, dbErr
		},
	}

	inboxRepository := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	events, err := inboxRepository.ListEventsByTenantID(ctx, "tenant-test")
	if err == nil {
		t.Fatal("expected query error, got nil")
	}
	if events != nil {
		t.Errorf("expected nil events slice on error, got %v", events)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestInboxRepository_ListEventsByTenantID_EmptyResult(t *testing.T) {
	mockDB, _ := sql.Open("postgres", "host=localhost port=1 user=dummy dbname=dummy sslmode=disable")
	_ = mockDB.Close()

	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			rows, _ := mockDB.QueryContext(ctx, "SELECT 1 WHERE 1=0")
			return rows, nil
		},
	}

	inboxRepository := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	events, err := inboxRepository.ListEventsByTenantID(ctx, "tenant-test")
	if err != nil {
		t.Fatalf("expected nil error on empty query result, got %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}
