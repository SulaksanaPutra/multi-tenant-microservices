package txcontext

import (
	"context"
	"database/sql"
	"fmt"
)

type execKey struct{}

// DBExecutor generalizes operations shared between *sql.DB and *sql.Tx.
type DBExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// TxManager executes operations within a database transaction boundary.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

type sqlTxManager struct {
	db *sql.DB
}

// NewTxManager returns a new TxManager instance backed by standard *sql.DB.
func NewTxManager(db *sql.DB) TxManager {
	return &sqlTxManager{db: db}
}

func (m *sqlTxManager) WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) (err error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	txCtx := WithTx(ctx, tx)

	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	err = fn(txCtx)
	return err
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
