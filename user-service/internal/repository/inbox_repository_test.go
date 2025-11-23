package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"user-service/internal/infrastructure/postgres"
	"user-service/internal/txcontext"
)


func TestInboxRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewInboxRepository(client)
	if repo == nil {
		t.Fatal("expected NewInboxRepository to return a non-nil struct pointer")
	}
}

func TestInboxRepository_TryInsert_NewEvent(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &mockDBExecutor{
		execContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return mockResult{rowsAffected: 1}, nil
		},
	}

	repo := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	eventID := "evt-unique-123"
	isDuplicate, err := repo.TryInsert(ctx, eventID)
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
	mockExec := &mockDBExecutor{
		execContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return mockResult{rowsAffected: 0}, nil
		},
	}

	repo := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	isDuplicate, err := repo.TryInsert(ctx, "evt-dup-456")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !isDuplicate {
		t.Errorf("expected isDuplicate to be true for existing event, got false")
	}
}

func TestInboxRepository_TryInsert_ExecError(t *testing.T) {
	dbErr := errors.New("exec error")
	mockExec := &mockDBExecutor{
		execContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	repo := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	isDuplicate, err := repo.TryInsert(ctx, "evt-err")
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
	mockExec := &mockDBExecutor{
		execContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return mockResult{rowsAffectedErr: raErr}, nil
		},
	}

	repo := NewInboxRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	isDuplicate, err := repo.TryInsert(ctx, "evt-ra-err")
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
