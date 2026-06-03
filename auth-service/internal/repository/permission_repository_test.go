package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"auth-service/internal/infrastructure/postgres"
	"auth-service/internal/testutil"
	"auth-service/internal/txcontext"
)

func TestPermissionRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewPermissionRepository(client)
	if repo == nil {
		t.Fatal("expected NewPermissionRepository to return non-nil struct pointer")
	}
}

func TestPermissionRepository_BulkUpsertPermissions(t *testing.T) {
	t.Run("empty items slice", func(t *testing.T) {
		repo := NewPermissionRepository(&postgres.Client{})
		err := repo.BulkUpsertPermissions(context.Background(), BulkUpsertPermissionsInput{})
		if err != nil {
			t.Errorf("expected nil error for empty items slice, got %v", err)
		}
	})

	t.Run("success upsert", func(t *testing.T) {
		var capturedQueries []string
		var capturedArgs [][]any

		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				capturedQueries = append(capturedQueries, query)
				capturedArgs = append(capturedArgs, args)
				return testutil.MockResult{RowsAffectedVal: 1}, nil
			},
		}

		repo := NewPermissionRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		items := []RegisterPermissionItem{
			{Name: "auth:read", Description: "Read auth data"},
			{Name: "auth:write", Description: "Write auth data"},
		}

		err := repo.BulkUpsertPermissions(ctxWithExec, BulkUpsertPermissionsInput{
			Service: "auth-service",
			Items:   items,
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		if len(capturedQueries) != 2 {
			t.Fatalf("expected 2 queries executed, got %d", len(capturedQueries))
		}

		for _, q := range capturedQueries {
			if !strings.Contains(q, "INSERT INTO public.permissions") || !strings.Contains(q, "ON CONFLICT (id) DO UPDATE") {
				t.Errorf("unexpected query string: %s", q)
			}
		}

		if len(capturedArgs[0]) != 3 || capturedArgs[0][0] != "auth:read" || capturedArgs[0][1] != "auth-service" {
			t.Errorf("unexpected args for item 0: %v", capturedArgs[0])
		}
	})

	t.Run("exec error", func(t *testing.T) {
		dbErr := errors.New("upsert failure")
		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return nil, dbErr
			},
		}

		repo := NewPermissionRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		items := []RegisterPermissionItem{{Name: "auth:read", Description: "Read auth"}}
		err := repo.BulkUpsertPermissions(ctxWithExec, BulkUpsertPermissionsInput{
			Service: "auth-service",
			Items:   items,
		})
		if err == nil || !errors.Is(err, dbErr) {
			t.Errorf("expected wrapped db error, got %v", err)
		}
	})
}

func TestPermissionRepository_ListAllPermissions(t *testing.T) {
	t.Run("query error", func(t *testing.T) {
		dbErr := errors.New("query list error")
		mockExec := &testutil.MockDBExecutor{
			QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
				return nil, dbErr
			},
		}

		repo := NewPermissionRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		perms, err := repo.ListAllPermissions(ctxWithExec)
		if perms != nil {
			t.Errorf("expected nil perms on query error, got %v", perms)
		}
		if err == nil || !errors.Is(err, dbErr) {
			t.Errorf("expected wrapped db error, got %v", err)
		}
	})

	t.Run("query string validation", func(t *testing.T) {
		var capturedQuery string
		mockExec := &testutil.MockDBExecutor{
			QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
				capturedQuery = query
				return testutil.GetDummyRow(ctx)
			},
		}

		repo := NewPermissionRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		// FindByName triggers QueryRowContext
		_, _ = repo.FindByName(ctxWithExec, "auth:read")

		if !strings.Contains(capturedQuery, "FROM public.permissions") || !strings.Contains(capturedQuery, "WHERE name = $1") {
			t.Errorf("unexpected query string: %s", capturedQuery)
		}
	})
}

func TestPermissionRepository_FindByIDs(t *testing.T) {
	t.Run("empty ids slice", func(t *testing.T) {
		repo := NewPermissionRepository(&postgres.Client{})
		perms, err := repo.FindByIDs(context.Background(), nil)
		if err != nil || perms != nil {
			t.Errorf("expected nil perms and nil error for empty ids, got perms=%v err=%v", perms, err)
		}
	})

	t.Run("query error", func(t *testing.T) {
		dbErr := errors.New("find by ids error")
		mockExec := &testutil.MockDBExecutor{
			QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
				return nil, dbErr
			},
		}

		repo := NewPermissionRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		perms, err := repo.FindByIDs(ctxWithExec, []string{"p1", "p2"})
		if perms != nil {
			t.Errorf("expected nil perms on query error, got %v", perms)
		}
		if err == nil || !errors.Is(err, dbErr) {
			t.Errorf("expected wrapped db error, got %v", err)
		}
	})
}

func TestPermissionRepository_FindByName(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			capturedArgs = args
			return testutil.GetDummyRow(ctx)
		},
	}

	repo := NewPermissionRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	perm, err := repo.FindByName(ctxWithExec, "auth:read")
	if perm != nil {
		t.Errorf("expected nil perm on scan error, got %+v", perm)
	}
	if err == nil {
		t.Fatal("expected scan error on dummy row, got nil")
	}

	if !strings.Contains(capturedQuery, "WHERE name = $1") {
		t.Errorf("unexpected query string: %s", capturedQuery)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "auth:read" {
		t.Errorf("unexpected query args: %v", capturedArgs)
	}
}
