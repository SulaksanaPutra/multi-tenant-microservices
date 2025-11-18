package txcontext

import (
	"context"
	"database/sql"
	"testing"
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

func TestGetExecutor_Fallback(t *testing.T) {
	ctx := context.Background()
	fallback := &mockExecutor{}

	exec := GetExecutor(ctx, fallback)
	if exec != fallback {
		t.Errorf("expected fallback executor, got different reference")
	}
}

func TestGetExecutor_FromContext(t *testing.T) {
	customExec := &mockExecutor{}
	ctx := WithExecutor(context.Background(), customExec)
	fallback := &mockExecutor{}

	exec := GetExecutor(ctx, fallback)
	if exec != customExec {
		t.Errorf("expected custom executor from context")
	}
}
