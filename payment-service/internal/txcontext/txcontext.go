package txcontext

import (
	"context"
	"database/sql"
	"fmt"
)

type execKey struct{}

type DBExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type SQLTxManager struct {
	db *sql.DB
}

func NewTxManager(db *sql.DB) *SQLTxManager {
	return &SQLTxManager{db: db}
}

func (m *SQLTxManager) WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) (err error) {
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

func WithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, execKey{}, DBExecutor(tx))
}

func WithExecutor(ctx context.Context, exec DBExecutor) context.Context {
	return context.WithValue(ctx, execKey{}, exec)
}

func GetExecutor(ctx context.Context, fallback DBExecutor) DBExecutor {
	if exec, ok := ctx.Value(execKey{}).(DBExecutor); ok && exec != nil {
		return exec
	}
	return fallback
}
