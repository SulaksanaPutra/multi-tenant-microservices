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
		res, err := mock.ExecContext(ctx, "UPDATE orders SET status = $1", "completed")
		if res != nil || err != nil {
			t.Errorf("expected nil result and nil error on default ExecContext, got %v, %v", res, err)
		}

		expectedErr := errors.New("exec error")
		mock.ExecContextFn = func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return MockResult{RowsAffectedVal: 5}, expectedErr
		}
		res, err = mock.ExecContext(ctx, "UPDATE orders SET status = $1", "completed")
		if err != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		rows, _ := res.RowsAffected()
		if rows != 5 {
			t.Errorf("expected 5 rows affected, got %d", rows)
		}
	})

	t.Run("QueryContext fallback and custom fn", func(t *testing.T) {
		mock := &MockDBExecutor{}
		rows, err := mock.QueryContext(ctx, "SELECT * FROM orders")
		if rows != nil || err != nil {
			t.Errorf("expected nil rows and nil error on default QueryContext, got %v, %v", rows, err)
		}

		mock.QueryContextFn = func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, errors.New("query error")
		}
		_, err = mock.QueryContext(ctx, "SELECT * FROM orders")
		if err == nil || err.Error() != "query error" {
			t.Errorf("expected query error, got %v", err)
		}
	})

	t.Run("QueryRowContext fallback returns dummy row", func(t *testing.T) {
		mock := &MockDBExecutor{}
		row := mock.QueryRowContext(ctx, "SELECT * FROM orders WHERE id = $1", 1)
		if row == nil {
			t.Fatal("expected non-nil row from GetDummyRow fallback")
		}
		// Scanning dummy row should return connection error rather than panic
		var id int
		if err := row.Scan(&id); err == nil {
			t.Error("expected error when scanning dummy row, got nil")
		}
	})

	t.Run("MockResult getters", func(t *testing.T) {
		res := MockResult{
			RowsAffectedVal:    10,
			RowsAffectedErrVal: nil,
			LastInsertIDVal:    42,
			LastInsertIDErrVal: nil,
		}

		id, err := res.LastInsertId()
		if id != 42 || err != nil {
			t.Errorf("expected LastInsertId 42/nil, got %d/%v", id, err)
		}

		affected, err := res.RowsAffected()
		if affected != 10 || err != nil {
			t.Errorf("expected RowsAffected 10/nil, got %d/%v", affected, err)
		}
	})
}
