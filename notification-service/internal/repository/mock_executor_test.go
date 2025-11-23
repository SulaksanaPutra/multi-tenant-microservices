package repository

import (
	"context"
	"database/sql"

	_ "github.com/lib/pq"
)

var dummyDB *sql.DB

func getDummyRow(ctx context.Context) *sql.Row {
	if dummyDB == nil {
		dummyDB, _ = sql.Open("postgres", "host=localhost port=1 user=dummy dbname=dummy sslmode=disable")
		_ = dummyDB.Close()
	}
	return dummyDB.QueryRowContext(ctx, "SELECT 1")
}

type mockDBExecutor struct {
	execContextFn     func(ctx context.Context, query string, args ...any) (sql.Result, error)
	queryContextFn    func(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	queryRowContextFn func(ctx context.Context, query string, args ...any) *sql.Row
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
	if m.queryRowContextFn != nil {
		return m.queryRowContextFn(ctx, query, args...)
	}
	return getDummyRow(ctx)
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
