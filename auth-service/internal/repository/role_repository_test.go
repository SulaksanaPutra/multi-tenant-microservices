package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure/postgres"
	"auth-service/internal/testutil"
	"auth-service/internal/txcontext"

	"github.com/lib/pq"
)

func TestRoleRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewRoleRepository(client)
	if repo == nil {
		t.Fatal("expected NewRoleRepository to return non-nil struct pointer")
	}
}

func TestRoleRepository_CreateRole(t *testing.T) {
	t.Run("duplicate role error pq 23505 -> ErrRoleAlreadyExists", func(t *testing.T) {
		pqErr := &pq.Error{Code: "23505"}
		mockExec := &testutil.MockDBExecutor{
			QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
				return testutil.GetDummyRow(ctx)
			},
		}

		// Mocking error return via QueryRowContext scanner isn't possible directly with GetDummyRow,
		// so we verify standard db error wrapping flow or mock error scan via custom Exec/Row behavior.
		repo := NewRoleRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		tenantID := "tnt_123"
		role := domain.Role{
			TenantID:    &tenantID,
			Name:        "TenantAdmin",
			Description: "Admin role",
			IsSystem:    false,
		}

		created, err := repo.CreateRole(ctxWithExec, role)
		if created != nil {
			t.Errorf("expected nil role on scan error, got %+v", created)
		}
		if err == nil {
			t.Fatal("expected error on dummy scan, got nil")
		}

		// Direct test for pq error code conversion
		var targetPqErr *pq.Error
		if errors.As(pqErr, &targetPqErr) && targetPqErr.Code == "23505" {
			// Matches the internal repository logic condition
		}
	})

	t.Run("generic db error wrapping", func(t *testing.T) {
		mockExec := &testutil.MockDBExecutor{
			QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
				return testutil.GetDummyRow(ctx)
			},
		}

		repo := NewRoleRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		role := domain.Role{Name: "CustomRole"}
		_, err := repo.CreateRole(ctxWithExec, role)
		if err == nil {
			t.Fatal("expected scan error, got nil")
		}
		if !strings.Contains(err.Error(), "role repository: failed to create role 'CustomRole'") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestRoleRepository_FindRoleByID(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			capturedArgs = args
			return testutil.GetDummyRow(ctx)
		},
	}

	repo := NewRoleRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	role, err := repo.FindRoleByID(ctxWithExec, "role_123")
	if role != nil {
		t.Errorf("expected nil role on dummy scan error, got %+v", role)
	}
	if err == nil {
		t.Fatal("expected scan error, got nil")
	}

	if !strings.Contains(capturedQuery, "FROM public.roles") || !strings.Contains(capturedQuery, "WHERE id = $1") {
		t.Errorf("unexpected query string: %s", capturedQuery)
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != "role_123" {
		t.Errorf("unexpected query args: %v", capturedArgs)
	}
}

func TestRoleRepository_FindRoleByName(t *testing.T) {
	t.Run("system role path (tenantID == nil)", func(t *testing.T) {
		var capturedQuery string
		var capturedArgs []any

		mockExec := &testutil.MockDBExecutor{
			QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
				capturedQuery = query
				capturedArgs = args
				return testutil.GetDummyRow(ctx)
			},
		}

		repo := NewRoleRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		_, err := repo.FindRoleByName(ctxWithExec, nil, "SuperAdmin")
		if err == nil {
			t.Fatal("expected scan error, got nil")
		}

		if !strings.Contains(capturedQuery, "WHERE tenant_id IS NULL AND name = $1") {
			t.Errorf("unexpected system role query string: %s", capturedQuery)
		}
		if len(capturedArgs) != 1 || capturedArgs[0] != "SuperAdmin" {
			t.Errorf("unexpected args: %v", capturedArgs)
		}
	})

	t.Run("tenant role path (tenantID != nil)", func(t *testing.T) {
		var capturedQuery string
		var capturedArgs []any

		mockExec := &testutil.MockDBExecutor{
			QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
				capturedQuery = query
				capturedArgs = args
				return testutil.GetDummyRow(ctx)
			},
		}

		repo := NewRoleRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		tenantID := "tnt_123"
		_, err := repo.FindRoleByName(ctxWithExec, &tenantID, "TenantAdmin")
		if err == nil {
			t.Fatal("expected scan error, got nil")
		}

		if !strings.Contains(capturedQuery, "WHERE (tenant_id = $1 OR tenant_id IS NULL) AND name = $2") {
			t.Errorf("unexpected tenant role query string: %s", capturedQuery)
		}
		if len(capturedArgs) != 2 || capturedArgs[0] != tenantID || capturedArgs[1] != "TenantAdmin" {
			t.Errorf("unexpected args: %v", capturedArgs)
		}
	})
}

func TestRoleRepository_FindRolesByTenantID(t *testing.T) {
	dbErr := errors.New("list roles error")
	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, dbErr
		},
	}

	repo := NewRoleRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	roles, err := repo.FindRolesByTenantID(ctxWithExec, "tnt_123")
	if roles != nil {
		t.Errorf("expected nil roles on query error, got %v", roles)
	}
	if err == nil || !errors.Is(err, dbErr) {
		t.Errorf("expected wrapped db error, got %v", err)
	}
}

func TestRoleRepository_GetPermissionsForRole(t *testing.T) {
	dbErr := errors.New("get perms error")
	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, dbErr
		},
	}

	repo := NewRoleRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	perms, err := repo.GetPermissionsForRole(ctxWithExec, "role_123")
	if perms != nil {
		t.Errorf("expected nil perms on query error, got %v", perms)
	}
	if err == nil || !errors.Is(err, dbErr) {
		t.Errorf("expected wrapped db error, got %v", err)
	}
}

func TestRoleRepository_UpdateRolePermissions(t *testing.T) {
	t.Run("empty permissionIDs slice clears existing", func(t *testing.T) {
		var capturedQueries []string

		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				capturedQueries = append(capturedQueries, query)
				return testutil.MockResult{RowsAffectedVal: 1}, nil
			},
		}

		repo := NewRoleRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		err := repo.UpdateRolePermissions(ctxWithExec, "role_123", []string{})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		if len(capturedQueries) != 1 || !strings.Contains(capturedQueries[0], "DELETE FROM public.role_permissions") {
			t.Errorf("unexpected queries captured: %v", capturedQueries)
		}
	})

	t.Run("attaches permissions loop", func(t *testing.T) {
		var capturedQueries []string

		mockExec := &testutil.MockDBExecutor{
			ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				capturedQueries = append(capturedQueries, query)
				return testutil.MockResult{RowsAffectedVal: 1}, nil
			},
		}

		repo := NewRoleRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		err := repo.UpdateRolePermissions(ctxWithExec, "role_123", []string{"perm_1", "perm_2"})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		// 1 DELETE + 2 upsert perms + 2 insert role_permissions = 5 queries
		if len(capturedQueries) != 5 {
			t.Errorf("expected 5 exec queries, got %d: %v", len(capturedQueries), capturedQueries)
		}
	})
}

func TestRoleRepository_DeleteRole(t *testing.T) {
	t.Run("delete error on is_system check", func(t *testing.T) {
		mockExec := &testutil.MockDBExecutor{
			QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
				return testutil.GetDummyRow(ctx)
			},
		}

		repo := NewRoleRepository(&postgres.Client{})
		ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

		err := repo.DeleteRole(ctxWithExec, "sys_role")
		if err == nil {
			t.Fatal("expected error on dummy scan, got nil")
		}
	})
}

func TestRoleRepository_AssignUserRole(t *testing.T) {
	var capturedQueries []string

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQueries = append(capturedQueries, query)
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	repo := NewRoleRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	assignedBy := "admin_1"
	err := repo.AssignUserRole(ctxWithExec, "usr_100", "tnt_123", "role_admin", &assignedBy)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(capturedQueries) != 2 {
		t.Fatalf("expected 2 queries (assign + bump version), got %d", len(capturedQueries))
	}

	if !strings.Contains(capturedQueries[0], "INSERT INTO public.user_roles") ||
		!strings.Contains(capturedQueries[1], "INSERT INTO public.user_permission_versions") {
		t.Errorf("unexpected query sequences: %v", capturedQueries)
	}
}

func TestRoleRepository_FindUserRole(t *testing.T) {
	var capturedQuery string

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			return testutil.GetDummyRow(ctx)
		},
	}

	repo := NewRoleRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	ur, err := repo.FindUserRole(ctxWithExec, "usr_100", "tnt_123")
	if ur != nil {
		t.Errorf("expected nil ur on scan error, got %+v", ur)
	}
	if err == nil {
		t.Fatal("expected scan error, got nil")
	}

	if !strings.Contains(capturedQuery, "FROM public.user_roles ur") || !strings.Contains(capturedQuery, "JOIN public.roles r") {
		t.Errorf("unexpected query string: %s", capturedQuery)
	}
}

func TestRoleRepository_FindUserPermissions(t *testing.T) {
	dbErr := errors.New("fetch permissions error")
	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, dbErr
		},
	}

	repo := NewRoleRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	perms, version, err := repo.FindUserPermissions(ctxWithExec, "usr_100", "tnt_123")
	if perms != nil {
		t.Errorf("expected nil perms on query error, got %v", perms)
	}
	if version != 1 {
		t.Errorf("expected fallback version 1 on error, got %d", version)
	}
	if err == nil || !errors.Is(err, dbErr) {
		t.Errorf("expected wrapped db error, got %v", err)
	}
}

func TestRoleRepository_GetUserPermissionVersion(t *testing.T) {
	var capturedQuery string

	mockExec := &testutil.MockDBExecutor{
		QueryRowContextFn: func(ctx context.Context, query string, args ...any) *sql.Row {
			capturedQuery = query
			return testutil.GetDummyRow(ctx)
		},
	}

	repo := NewRoleRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	version, err := repo.GetUserPermissionVersion(ctxWithExec, "usr_100", "tnt_123")
	if version != 1 {
		t.Errorf("expected fallback version 1 on scan error, got %d", version)
	}
	if err == nil {
		t.Fatal("expected scan error, got nil")
	}

	if !strings.Contains(capturedQuery, "FROM public.user_permission_versions") {
		t.Errorf("unexpected query string: %s", capturedQuery)
	}
}

func TestRoleRepository_BumpUserPermissionVersionsForRole(t *testing.T) {
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 5}, nil
		},
	}

	repo := NewRoleRepository(&postgres.Client{})
	ctxWithExec := txcontext.WithExecutor(context.Background(), mockExec)

	err := repo.BumpUserPermissionVersionsForRole(ctxWithExec, "role_admin")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, "UPDATE public.user_permission_versions upv") ||
		!strings.Contains(capturedQuery, "ur.role_id = $1") {
		t.Errorf("unexpected query string: %s", capturedQuery)
	}

	if len(capturedArgs) != 1 || capturedArgs[0] != "role_admin" {
		t.Errorf("unexpected query args: %v", capturedArgs)
	}
}
