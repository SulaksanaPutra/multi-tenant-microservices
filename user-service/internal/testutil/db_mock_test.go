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
		res, err := mock.ExecContext(ctx, "DELETE FROM users WHERE id = $1", 1)
		if res != nil || err != nil {
			t.Errorf("expected nil result and nil error on default ExecContext, got %v, %v", res, err)
		}

		expectedErr := errors.New("exec error")
		mock.ExecContextFn = func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return MockResult{RowsAffectedVal: 1}, expectedErr
		}
		res, err = mock.ExecContext(ctx, "DELETE FROM users WHERE id = $1", 1)
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
		rows, err := mock.QueryContext(ctx, "SELECT * FROM users")
		if rows != nil || err != nil {
			t.Errorf("expected nil rows and nil error on default QueryContext, got %v, %v", rows, err)
		}

		mock.QueryContextFn = func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, errors.New("query error")
		}
		_, err = mock.QueryContext(ctx, "SELECT * FROM users")
		if err == nil || err.Error() != "query error" {
			t.Errorf("expected query error, got %v", err)
		}
	})

	t.Run("QueryRowContext fallback returns dummy row", func(t *testing.T) {
		mock := &MockDBExecutor{}
		row := mock.QueryRowContext(ctx, "SELECT * FROM users WHERE id = $1", 1)
		if row == nil {
			t.Fatal("expected non-nil row from GetDummyRow fallback")
		}
		var id int
		if err := row.Scan(&id); err == nil {
			t.Error("expected error when scanning dummy row, got nil")
		}
	})

	t.Run("MockResult getters", func(t *testing.T) {
		res := MockResult{
			RowsAffectedVal:    1,
			RowsAffectedErrVal: nil,
			LastInsertIDVal:    505,
			LastInsertIDErrVal: nil,
		}

		id, err := res.LastInsertId()
		if id != 505 || err != nil {
			t.Errorf("expected LastInsertId 505/nil, got %d/%v", id, err)
		}

		affected, err := res.RowsAffected()
		if affected != 1 || err != nil {
			t.Errorf("expected RowsAffected 1/nil, got %d/%v", affected, err)
		}
	})
}
