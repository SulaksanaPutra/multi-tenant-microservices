package testutil

import (
	"context"
	"database/sql"

	_ "github.com/lib/pq"
)

var dummyDB *sql.DB

// GetDummyRow returns a valid non-nil *sql.Row with an underlying connection error,
// safely avoiding nil pointer panics when .Scan() is called in tests.
func GetDummyRow(ctx context.Context) *sql.Row {
	if dummyDB == nil {
		dummyDB, _ = sql.Open("postgres", "host=localhost port=1 user=dummy dbname=dummy sslmode=disable")
		_ = dummyDB.Close()
	}
	return dummyDB.QueryRowContext(ctx, "SELECT 1")
}

type MockDBExecutor struct {
	ExecContextFn     func(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContextFn    func(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContextFn func(ctx context.Context, query string, args ...any) *sql.Row
}

func (m *MockDBExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if m.ExecContextFn != nil {
		return m.ExecContextFn(ctx, query, args...)
	}
	return nil, nil
}

func (m *MockDBExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if m.QueryContextFn != nil {
		return m.QueryContextFn(ctx, query, args...)
	}
	return nil, nil
}

func (m *MockDBExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if m.QueryRowContextFn != nil {
		return m.QueryRowContextFn(ctx, query, args...)
	}
	return GetDummyRow(ctx)
}

type MockResult struct {
	RowsAffectedVal    int64
	RowsAffectedErrVal error
	LastInsertIDVal    int64
	LastInsertIDErrVal error
}

func (m MockResult) LastInsertId() (int64, error) {
	return m.LastInsertIDVal, m.LastInsertIDErrVal
}

func (m MockResult) RowsAffected() (int64, error) {
	return m.RowsAffectedVal, m.RowsAffectedErrVal
}
