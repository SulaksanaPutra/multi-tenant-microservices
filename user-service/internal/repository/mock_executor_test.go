package repository

import (
	"context"
	"database/sql"
)

type mockDBExecutor struct {
	execContextFn  func(ctx context.Context, query string, args ...any) (sql.Result, error)
	queryContextFn func(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (m *mockDBExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if m.execContextFn != nil {
		return m.execContextFn(ctx, query, args...)
	}
	return nil, nil
}

func (m *mockDBExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if m.queryContextFn != nil {
		return m.queryContextFn(ctx, query, args...)
	}
	return nil, nil
}

func (m *mockDBExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return nil
}

type mockResult struct {
	rowsAffected    int64
	rowsAffectedErr error
	lastInsertID    int64
	lastInsertIDErr error
}

func (m mockResult) LastInsertId() (int64, error) {
	return m.lastInsertID, m.lastInsertIDErr
}

func (m mockResult) RowsAffected() (int64, error) {
	return m.rowsAffected, m.rowsAffectedErr
}
