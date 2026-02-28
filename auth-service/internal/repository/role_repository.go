package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure/postgres"
	"auth-service/internal/txcontext"

	"github.com/lib/pq"
)

type RoleRepository struct {
	dbClient *postgres.Client
}

func NewRoleRepository(dbClient *postgres.Client) *RoleRepository {
	return &RoleRepository{dbClient: dbClient}
}

func (r *RoleRepository) CreateRole(ctx context.Context, role domain.Role) (*domain.Role, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		INSERT INTO public.roles (tenant_id, name, description, is_system)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at;
	`
	var created domain.Role
	created = role
	var tenantIDVal *string
	if role.TenantID != nil && *role.TenantID != "" {
		tenantIDVal = role.TenantID
	}

	if err := exec.QueryRowContext(ctx, query, tenantIDVal, role.Name, role.Description, role.IsSystem).Scan(&created.ID, &created.CreatedAt); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" { // unique_violation
			return nil, domain.ErrRoleAlreadyExists
		}
		return nil, fmt.Errorf("role repository: failed to create role '%s': %w", role.Name, err)
	}

	return &created, nil
}

func (r *RoleRepository) FindRoleByID(ctx context.Context, id string) (*domain.Role, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT id, tenant_id, name, COALESCE(description, ''), is_system, created_at
		FROM public.roles
		WHERE id = $1;
	`
	var role domain.Role
	var tenantID sql.NullString
	if err := exec.QueryRowContext(ctx, query, id).Scan(&role.ID, &tenantID, &role.Name, &role.Description, &role.IsSystem, &role.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrRoleNotFound
		}
		return nil, fmt.Errorf("role repository: failed to find role by id '%s': %w", id, err)
	}
	if tenantID.Valid {
		role.TenantID = &tenantID.String
	}

	// Fetch permissions attached to this role
	perms, err := r.GetPermissionsForRole(ctx, id)
	if err != nil {
		return nil, err
	}
	role.Permissions = perms

	return &role, nil
}

func (r *RoleRepository) FindRoleByName(ctx context.Context, tenantID *string, name string) (*domain.Role, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	var query string
	var args []any

	if tenantID == nil || *tenantID == "" {
		query = `
			SELECT id, tenant_id, name, COALESCE(description, ''), is_system, created_at
			FROM public.roles
			WHERE tenant_id IS NULL AND name = $1;
		`
		args = []any{name}
	} else {
		query = `
			SELECT id, tenant_id, name, COALESCE(description, ''), is_system, created_at
			FROM public.roles
			WHERE (tenant_id = $1 OR tenant_id IS NULL) AND name = $2
			ORDER BY tenant_id NULLS LAST
			LIMIT 1;
		`
		args = []any{*tenantID, name}
	}

	var role domain.Role
	var tenantIDVal sql.NullString
	if err := exec.QueryRowContext(ctx, query, args...).Scan(&role.ID, &tenantIDVal, &role.Name, &role.Description, &role.IsSystem, &role.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrRoleNotFound
		}
		return nil, fmt.Errorf("role repository: failed to find role by name '%s': %w", name, err)
	}
	if tenantIDVal.Valid {
		role.TenantID = &tenantIDVal.String
	}

	perms, err := r.GetPermissionsForRole(ctx, role.ID)
	if err != nil {
		return nil, err
	}
	role.Permissions = perms

	return &role, nil
}

func (r *RoleRepository) FindRolesByTenantID(ctx context.Context, tenantID string) ([]domain.Role, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT id, tenant_id, name, COALESCE(description, ''), is_system, created_at
		FROM public.roles
		WHERE tenant_id = $1 OR tenant_id IS NULL
		ORDER BY is_system DESC, name ASC;
	`
	rows, err := exec.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("role repository: failed to list roles for tenant '%s': %w", tenantID, err)
	}
	defer rows.Close()

	var roles []domain.Role
	for rows.Next() {
		var role domain.Role
		var tenantIDVal sql.NullString
		if err := rows.Scan(&role.ID, &tenantIDVal, &role.Name, &role.Description, &role.IsSystem, &role.CreatedAt); err != nil {
			return nil, fmt.Errorf("role repository: failed to scan role: %w", err)
		}
		if tenantIDVal.Valid {
			role.TenantID = &tenantIDVal.String
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("role repository: rows error: %w", err)
	}
	return roles, nil
}

func (r *RoleRepository) GetPermissionsForRole(ctx context.Context, roleID string) ([]domain.Permission, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT p.id, p.name, p.service, COALESCE(p.description, ''), p.created_at
		FROM public.permissions p
		JOIN public.role_permissions rp ON rp.permission_id = p.id
		WHERE rp.role_id = $1
		ORDER BY p.service, p.name;
	`
	rows, err := exec.QueryContext(ctx, query, roleID)
	if err != nil {
		return nil, fmt.Errorf("role repository: failed to fetch permissions for role_id '%s': %w", roleID, err)
	}
	defer rows.Close()

	var permissions []domain.Permission
	for rows.Next() {
		var p domain.Permission
		if err := rows.Scan(&p.ID, &p.Name, &p.Service, &p.Description, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("role repository: failed to scan role permission: %w", err)
		}
		permissions = append(permissions, p)
	}
	return permissions, nil
}

func (r *RoleRepository) UpdateRolePermissions(ctx context.Context, roleID string, permissionIDs []string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)

	// Delete existing permissions for role
	if _, err := exec.ExecContext(ctx, `DELETE FROM public.role_permissions WHERE role_id = $1;`, roleID); err != nil {
		return fmt.Errorf("role repository: failed to clear role permissions for role_id '%s': %w", roleID, err)
	}

	if len(permissionIDs) == 0 {
		return nil
	}

	permUpsertQuery := `
		INSERT INTO public.permissions (id, name, service, description)
		VALUES ($1, $1, 'custom', 'Custom Role Permission')
		ON CONFLICT (id) DO NOTHING;
	`

	// Insert new permissions
	insertQuery := `
		INSERT INTO public.role_permissions (role_id, permission_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING;
	`
	for _, pid := range permissionIDs {
		if pid == "" {
			continue
		}
		_, _ = exec.ExecContext(ctx, permUpsertQuery, pid)
		if _, err := exec.ExecContext(ctx, insertQuery, roleID, pid); err != nil {
			return fmt.Errorf("role repository: failed to attach permission_id '%s' to role_id '%s': %w", pid, roleID, err)
		}
	}
	return nil
}

func (r *RoleRepository) DeleteRole(ctx context.Context, id string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)

	// Verify not system role
	var isSystem bool
	if err := exec.QueryRowContext(ctx, `SELECT is_system FROM public.roles WHERE id = $1;`, id).Scan(&isSystem); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrRoleNotFound
		}
		return fmt.Errorf("role repository: failed to check is_system for role_id '%s': %w", id, err)
	}

	if isSystem {
		return domain.ErrSystemRoleProtected
	}

	if _, err := exec.ExecContext(ctx, `DELETE FROM public.roles WHERE id = $1;`, id); err != nil {
		return fmt.Errorf("role repository: failed to delete role_id '%s': %w", id, err)
	}
	return nil
}

func (r *RoleRepository) AssignUserRole(ctx context.Context, userID, tenantID, roleID string, assignedBy *string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		INSERT INTO public.user_roles (user_id, tenant_id, role_id, assigned_by, assigned_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id, tenant_id) DO UPDATE
		SET role_id     = EXCLUDED.role_id,
		    assigned_by = EXCLUDED.assigned_by,
		    assigned_at = NOW();
	`
	if _, err := exec.ExecContext(ctx, query, userID, tenantID, roleID, assignedBy); err != nil {
		return fmt.Errorf("role repository: failed to assign role_id '%s' to user_id '%s': %w", roleID, userID, err)
	}

	// Also ensure user_permission_versions row exists
	vQuery := `
		INSERT INTO public.user_permission_versions (user_id, tenant_id, version, updated_at)
		VALUES ($1, $2, 1, NOW())
		ON CONFLICT (user_id, tenant_id) DO UPDATE
		SET version    = public.user_permission_versions.version + 1,
		    updated_at = NOW();
	`
	if _, err := exec.ExecContext(ctx, vQuery, userID, tenantID); err != nil {
		return fmt.Errorf("role repository: failed to bump user_permission_versions for user_id '%s': %w", userID, err)
	}

	return nil
}

func (r *RoleRepository) FindUserRole(ctx context.Context, userID, tenantID string) (*domain.UserRole, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT ur.user_id, ur.tenant_id, ur.role_id, ur.assigned_at, ur.assigned_by,
		       r.id, r.tenant_id, r.name, COALESCE(r.description, ''), r.is_system, r.created_at
		FROM public.user_roles ur
		JOIN public.roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1 AND ur.tenant_id = $2;
	`
	var ur domain.UserRole
	var role domain.Role
	var assignedBy sql.NullString
	var roleTenantID sql.NullString

	row := exec.QueryRowContext(ctx, query, userID, tenantID)
	if err := row.Scan(
		&ur.UserID, &ur.TenantID, &ur.RoleID, &ur.AssignedAt, &assignedBy,
		&role.ID, &roleTenantID, &role.Name, &role.Description, &role.IsSystem, &role.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrRoleNotFound
		}
		return nil, fmt.Errorf("role repository: failed to find user role: %w", err)
	}

	if assignedBy.Valid {
		ur.AssignedBy = &assignedBy.String
	}
	if roleTenantID.Valid {
		role.TenantID = &roleTenantID.String
	}
	ur.Role = &role

	perms, err := r.GetPermissionsForRole(ctx, role.ID)
	if err != nil {
		return nil, err
	}
	role.Permissions = perms

	return &ur, nil
}

func (r *RoleRepository) FindUserPermissions(ctx context.Context, userID, tenantID string) ([]string, int64, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)

	// Fetch permission names
	query := `
		SELECT DISTINCT p.name
		FROM public.permissions p
		JOIN public.role_permissions rp ON rp.permission_id = p.id
		JOIN public.user_roles ur ON ur.role_id = rp.role_id
		WHERE ur.user_id = $1 AND ur.tenant_id = $2
		ORDER BY p.name;
	`
	rows, err := exec.QueryContext(ctx, query, userID, tenantID)
	if err != nil {
		return nil, 1, fmt.Errorf("role repository: failed to fetch permissions for user_id '%s': %w", userID, err)
	}
	defer rows.Close()

	var permissions []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, 1, fmt.Errorf("role repository: failed to scan permission name: %w", err)
		}
		permissions = append(permissions, name)
	}

	// Fetch version
	var version int64 = 1
	vQuery := `SELECT version FROM public.user_permission_versions WHERE user_id = $1 AND tenant_id = $2;`
	if err := exec.QueryRowContext(ctx, vQuery, userID, tenantID).Scan(&version); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, 1, fmt.Errorf("role repository: failed to fetch user_permission_versions: %w", err)
		}
	}

	return permissions, version, nil
}

func (r *RoleRepository) GetUserPermissionVersion(ctx context.Context, userID, tenantID string) (int64, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	var version int64 = 1
	query := `SELECT version FROM public.user_permission_versions WHERE user_id = $1 AND tenant_id = $2;`
	if err := exec.QueryRowContext(ctx, query, userID, tenantID).Scan(&version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 1, nil
		}
		return 1, fmt.Errorf("role repository: failed to get user_permission_versions: %w", err)
	}
	return version, nil
}

func (r *RoleRepository) BumpUserPermissionVersionsForRole(ctx context.Context, roleID string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		UPDATE public.user_permission_versions upv
		SET version    = upv.version + 1,
		    updated_at = NOW()
		FROM public.user_roles ur
		WHERE ur.user_id = upv.user_id
		  AND ur.tenant_id = upv.tenant_id
		  AND ur.role_id = $1;
	`
	if _, err := exec.ExecContext(ctx, query, roleID); err != nil {
		return fmt.Errorf("role repository: failed to bump user permission versions for role_id '%s': %w", roleID, err)
	}
	return nil
}
