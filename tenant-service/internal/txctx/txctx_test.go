package txctx_test

import (
	"context"
	"database/sql"
	"testing"

	"tenant-service/internal/txctx"
)

type dummyExec struct{}

func TestTxContextFunctions(t *testing.T) {
	ctx := context.Background()

	// Default fallback
	fallback := &dummyExec{}
	if got := txctx.GetExecutor(ctx, fallback); got != fallback {
		t.Errorf("GetExecutor with fallback failed, expected fallback instance")
	}

	// Context with executor
	exec := &dummyExec{}
	ctxWithExec := txctx.WithExecutor(ctx, exec)
	if got := txctx.GetExecutor(ctxWithExec, fallback); got != exec {
		t.Errorf("GetExecutor with context executor failed, expected context instance")
	}
}

var _ txctx.DBExecutor = (*dummyExec)(nil)

func (d *dummyExec) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return nil, nil
}
func (d *dummyExec) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return nil, nil
}
func (d *dummyExec) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return nil
}
