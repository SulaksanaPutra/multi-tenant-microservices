package txcontext

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
)

type mockExecutor struct{}

func (m *mockExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return nil, nil
}
func (m *mockExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return nil, nil
}
func (m *mockExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return nil
}

func TestNewTxManager(t *testing.T) {
	db := &sql.DB{}
	mgr := NewTxManager(db)
	if mgr == nil {
		t.Fatal("expected NewTxManager to return non-nil struct pointer")
	}
}

func TestGetExecutor_Fallback(t *testing.T) {
	ctx := context.Background()
	fallback := &mockExecutor{}

	exec := GetExecutor(ctx, fallback)
	if exec != fallback {
		t.Errorf("expected fallback executor, got different reference")
	}
}

func TestGetExecutor_FromContext_WithExecutor(t *testing.T) {
	customExec := &mockExecutor{}
	ctx := WithExecutor(context.Background(), customExec)
	fallback := &mockExecutor{}

	exec := GetExecutor(ctx, fallback)
	if exec != customExec {
		t.Errorf("expected custom executor from context, got fallback or nil")
	}
}

func TestGetExecutor_NilInContext(t *testing.T) {
	ctx := WithExecutor(context.Background(), nil)
	fallback := &mockExecutor{}

	exec := GetExecutor(ctx, fallback)
	if exec != fallback {
		t.Errorf("expected fallback executor when nil executor is stored in context")
	}
}

func TestWithTransaction_BeginTxError(t *testing.T) {
	dummyDB, err := sql.Open("postgres", "host=localhost port=1 user=dummy dbname=dummy sslmode=disable")
	if err != nil {
		t.Fatalf("failed to open dummy db: %v", err)
	}
	_ = dummyDB.Close()

	mgr := NewTxManager(dummyDB)
	err = mgr.WithTransaction(context.Background(), func(txCtx context.Context) error {
		return nil
	})

	if err == nil {
		t.Fatal("expected error from WithTransaction on closed DB, got nil")
	}
}
