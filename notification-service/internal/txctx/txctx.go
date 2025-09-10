package txctx

import (
	"context"
	"database/sql"
)

type execKey struct{}

// DBExecutor generalizes operations shared between *sql.DB and *sql.Tx.
type DBExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// WithTx returns a new Context that carries the provided *sql.Tx.
func WithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, execKey{}, DBExecutor(tx))
}

// WithExecutor returns a new Context that carries any custom DBExecutor (e.g. *sql.DB pool or *sql.Tx).
func WithExecutor(ctx context.Context, exec DBExecutor) context.Context {
	return context.WithValue(ctx, execKey{}, exec)
}

// GetExecutor extracts DBExecutor from Context if present; otherwise returns fallback DB.
func GetExecutor(ctx context.Context, fallback DBExecutor) DBExecutor {
	if exec, ok := ctx.Value(execKey{}).(DBExecutor); ok && exec != nil {
		return exec
	}
	return fallback
}
