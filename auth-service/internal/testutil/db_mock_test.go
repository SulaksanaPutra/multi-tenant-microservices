package testutil

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestMockDBExecutor(t *testing.T) {
	ctx := context.Background()
	execCalled := false
	queryCalled := false
	queryRowCalled := false

	mockExec := &MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			execCalled = true
			return MockResult{RowsAffectedVal: 1}, nil
		},
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			queryCalled = true
			return nil, nil
		},
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			queryRowCalled = true
			return GetDummyRow(ctx)
		},
	}

	res, err := mockExec.ExecContext(ctx, "UPDATE tbl SET x = 1")
	if err != nil || !execCalled {
		t.Fatalf("expected ExecContext to be called cleanly, err: %v", err)
	}
	rows, _ := res.RowsAffected()
	if rows != 1 {
		t.Errorf("expected 1 row affected, got %d", rows)
	}

	_, _ = mockExec.QueryContext(ctx, "SELECT * FROM tbl")
	if !queryCalled {
		t.Error("expected QueryContext to be called")
	}

	row := mockExec.QueryRowContext(ctx, "SELECT 1")
	if !queryRowCalled || row == nil {
		t.Error("expected QueryRowContext to return valid non-nil row")
	}
}

func TestMockResult(t *testing.T) {
	res := MockResult{
		RowsAffectedVal:    5,
		RowsAffectedErrVal: errors.New("rows err"),
		LastInsertIDVal:    10,
		LastInsertIDErrVal: errors.New("insert id err"),
	}

	id, err := res.LastInsertId()
	if id != 10 || err == nil {
		t.Errorf("unexpected LastInsertId result: %d, %v", id, err)
	}

	rows, err := res.RowsAffected()
	if rows != 5 || err == nil {
		t.Errorf("unexpected RowsAffected result: %d, %v", rows, err)
	}
}
