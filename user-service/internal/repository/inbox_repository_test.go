package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"user-service/internal/infrastructure/postgres"
	"user-service/internal/testutil"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

func TestInboxRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	inboxRepository := NewInboxRepository(client)
	if inboxRepository == nil {
		t.Fatal("expected NewInboxRepository to return a non-nil struct pointer")
	}
}

func TestInboxRepository_TryInsert_NewEvent(t *testing.T) {
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

	eventID := "evt-unique-123"
	isDuplicate, err := inboxRepository.TryInsert(ctx, eventID)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if isDuplicate {
		t.Errorf("expected isDuplicate to be false for new event, got true")
	}

	if !strings.Contains(capturedQuery, "INSERT INTO public.inbox") {
		t.Errorf("expected query to contain 'INSERT INTO public.inbox', got: %s", capturedQuery)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != eventID {
		t.Errorf("unexpected captured args: %v", capturedArgs)
	}
}

func TestInboxRepository_TryInsert_DuplicateEvent(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return testutil.MockResult{RowsAffectedVal: 0}, nil
		},
	}

	inboxRepository := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	isDuplicate, err := inboxRepository.TryInsert(ctx, "evt-dup-456")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !isDuplicate {
		t.Errorf("expected isDuplicate to be true for existing event, got false")
	}
}

func TestInboxRepository_TryInsert_ExecError(t *testing.T) {
	dbErr := errors.New("exec error")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	inboxRepository := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	isDuplicate, err := inboxRepository.TryInsert(ctx, "evt-err")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if isDuplicate {
		t.Errorf("expected isDuplicate to be false on error, got true")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestInboxRepository_TryInsert_RowsAffectedError(t *testing.T) {
	raErr := errors.New("rows affected fail")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return testutil.MockResult{RowsAffectedErrVal: raErr}, nil
		},
	}

	inboxRepository := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	isDuplicate, err := inboxRepository.TryInsert(ctx, "evt-ra-err")
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
