package testutil

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestMockDBExecutor(t *testing.T) {
	ctx := context.Background()

	t.Run("ExecContext fallback and custom fn", func(t *testing.T) {
		mock := &MockDBExecutor{}
		res, err := mock.ExecContext(ctx, "INSERT INTO tenants DEFAULT VALUES")
		if res != nil || err != nil {
			t.Errorf("expected nil result and nil error on default ExecContext, got %v, %v", res, err)
		}

		expectedErr := errors.New("exec error")
		mock.ExecContextFn = func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return MockResult{RowsAffectedVal: 1}, expectedErr
		}
		res, err = mock.ExecContext(ctx, "INSERT INTO tenants DEFAULT VALUES")
		if err != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		rows, _ := res.RowsAffected()
		if rows != 1 {
			t.Errorf("expected 1 row affected, got %d", rows)
		}
	})

	t.Run("QueryContext fallback and custom fn", func(t *testing.T) {
		mock := &MockDBExecutor{}
		rows, err := mock.QueryContext(ctx, "SELECT * FROM tenants")
		if rows != nil || err != nil {
			t.Errorf("expected nil rows and nil error on default QueryContext, got %v, %v", rows, err)
		}

		mock.QueryContextFn = func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, errors.New("query error")
		}
		_, err = mock.QueryContext(ctx, "SELECT * FROM tenants")
		if err == nil || err.Error() != "query error" {
			t.Errorf("expected query error, got %v", err)
		}
	})

	t.Run("QueryRowContext fallback returns dummy row", func(t *testing.T) {
		mock := &MockDBExecutor{}
		row := mock.QueryRowContext(ctx, "SELECT * FROM tenants WHERE id = $1", "tenant-1")
		if row == nil {
			t.Fatal("expected non-nil row from GetDummyRow fallback")
		}
		var id string
		if err := row.Scan(&id); err == nil {
			t.Error("expected error when scanning dummy row, got nil")
		}
	})

	t.Run("MockResult getters", func(t *testing.T) {
		res := MockResult{
			RowsAffectedVal:    3,
			RowsAffectedErrVal: nil,
			LastInsertIDVal:    77,
			LastInsertIDErrVal: nil,
		}

		id, err := res.LastInsertId()
		if id != 77 || err != nil {
			t.Errorf("expected LastInsertId 77/nil, got %d/%v", id, err)
		}

		affected, err := res.RowsAffected()
		if affected != 3 || err != nil {
			t.Errorf("expected RowsAffected 3/nil, got %d/%v", affected, err)
		}
	})
}
