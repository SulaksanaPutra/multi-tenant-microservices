package service_test

import (
	"context"
	"testing"

	"auth-service/internal/domain"
	"auth-service/internal/repository"
	"auth-service/internal/service"
)

type mockPermRepo struct {
	permissions []domain.Permission
}

func (m *mockPermRepo) BulkUpsertPermissions(_ context.Context, serviceName string, items []repository.RegisterPermissionItem) error {
	for _, item := range items {
		m.permissions = append(m.permissions, domain.Permission{
			ID:          "perm_" + item.Name,
			Name:        item.Name,
			Service:     serviceName,
			Description: item.Description,
		})
	}
	return nil
}

func (m *mockPermRepo) ListAllPermissions(_ context.Context) ([]domain.Permission, error) {
	return m.permissions, nil
}

func (m *mockPermRepo) FindByIDs(_ context.Context, ids []string) ([]domain.Permission, error) {
	var res []domain.Permission
	for _, p := range m.permissions {
		for _, id := range ids {
			if p.ID == id {
				res = append(res, p)
			}
		}
	}
	return res, nil
}

func (m *mockPermRepo) FindByName(_ context.Context, name string) (*domain.Permission, error) {
	for _, p := range m.permissions {
		if p.Name == name {
			return &p, nil
		}
	}
	return nil, domain.ErrPermissionNotFound
}

type mockRoleRepo struct {
	roles     map[string]*domain.Role
	userRoles map[string]*domain.UserRole
	versions  map[string]int64
}

func newMockRoleRepo() *mockRoleRepo {
	return &mockRoleRepo{
		roles:     make(map[string]*domain.Role),
		userRoles: make(map[string]*domain.UserRole),
		versions:  make(map[string]int64),
	}
}

func (m *mockRoleRepo) CreateRole(_ context.Context, role domain.Role) (*domain.Role, error) {
	r := role
	r.ID = "role_" + role.Name
	m.roles[r.ID] = &r
	return &r, nil
}

func (m *mockRoleRepo) FindRoleByID(_ context.Context, id string) (*domain.Role, error) {
	if r, ok := m.roles[id]; ok {
		return r, nil
	}
	return nil, domain.ErrRoleNotFound
}

func (m *mockRoleRepo) FindRoleByName(_ context.Context, tenantID *string, name string) (*domain.Role, error) {
	for _, r := range m.roles {
		if r.Name == name {
			return r, nil
		}
	}
	return nil, domain.ErrRoleNotFound
}

func (m *mockRoleRepo) FindRolesByTenantID(_ context.Context, tenantID string) ([]domain.Role, error) {
	var res []domain.Role
	for _, r := range m.roles {
		if r.TenantID != nil && *r.TenantID == tenantID {
			res = append(res, *r)
		}
	}
	return res, nil
}

func (m *mockRoleRepo) UpdateRolePermissions(_ context.Context, roleID string, permissionIDs []string) error {
	r, ok := m.roles[roleID]
	if !ok {
		return domain.ErrRoleNotFound
	}
	var perms []domain.Permission
	for _, pid := range permissionIDs {
		perms = append(perms, domain.Permission{ID: pid, Name: "perm_" + pid})
	}
	r.Permissions = perms
	return nil
}

func (m *mockRoleRepo) DeleteRole(_ context.Context, id string) error {
	r, ok := m.roles[id]
	if !ok {
		return domain.ErrRoleNotFound
	}
	if r.IsSystem {
		return domain.ErrSystemRoleProtected
	}
	delete(m.roles, id)
	return nil
}

func (m *mockRoleRepo) AssignUserRole(_ context.Context, userID, tenantID, roleID string, assignedBy *string) error {
	r, ok := m.roles[roleID]
	if !ok {
		return domain.ErrRoleNotFound
	}
	key := userID + "_" + tenantID
	m.userRoles[key] = &domain.UserRole{
		UserID:     userID,
		TenantID:   tenantID,
		RoleID:     roleID,
		AssignedBy: assignedBy,
		Role:       r,
	}
	m.versions[key] = 1
	return nil
}

func (m *mockRoleRepo) FindUserRole(_ context.Context, userID, tenantID string) (*domain.UserRole, error) {
	key := userID + "_" + tenantID
	if ur, ok := m.userRoles[key]; ok {
		return ur, nil
	}
	return nil, domain.ErrRoleNotFound
}

func (m *mockRoleRepo) FindUserPermissions(_ context.Context, userID, tenantID string) ([]string, int64, error) {
	key := userID + "_" + tenantID
	ur, ok := m.userRoles[key]
	if !ok || ur.Role == nil {
		return nil, 1, nil
	}
	var res []string
	for _, p := range ur.Role.Permissions {
		res = append(res, p.Name)
	}
	ver := m.versions[key]
	if ver == 0 {
		ver = 1
	}
	return res, ver, nil
}

func (m *mockRoleRepo) GetUserPermissionVersion(_ context.Context, userID, tenantID string) (int64, error) {
	key := userID + "_" + tenantID
	ver, ok := m.versions[key]
	if !ok {
		return 1, nil
	}
	return ver, nil
}

func (m *mockRoleRepo) BumpUserPermissionVersionsForRole(_ context.Context, roleID string) error {
	for k, ur := range m.userRoles {
		if ur.RoleID == roleID {
			m.versions[k]++
		}
	}
	return nil
}

func TestPermissionService_RegisterAndList(t *testing.T) {
	pRepo := &mockPermRepo{}
	rRepo := newMockRoleRepo()
	svc := service.NewPermissionService(pRepo, rRepo)
	ctx := context.Background()

	err := svc.RegisterPermissions(ctx, service.RegisterPermissionsInput{
		Service: "order-service",
		Permissions: []repository.RegisterPermissionItem{
			{Name: "orders:create", Description: "Create order"},
			{Name: "orders:read", Description: "Read order"},
		},
	})
	if err != nil {
		t.Fatalf("RegisterPermissions failed: %v", err)
	}

	perms, err := svc.ListPermissions(ctx)
	if err != nil {
		t.Fatalf("ListPermissions failed: %v", err)
	}
	if len(perms) != 2 {
		t.Fatalf("expected 2 permissions, got %d", len(perms))
	}
}

func TestPermissionService_CreateAndManageRole(t *testing.T) {
	pRepo := &mockPermRepo{}
	rRepo := newMockRoleRepo()
	svc := service.NewPermissionService(pRepo, rRepo)
	ctx := context.Background()

	tenantID := "tnt_001"
	role, err := svc.CreateRole(ctx, service.CreateRoleInput{
		TenantID:    tenantID,
		Name:        "custom_manager",
		Description: "Custom Manager Role",
	})
	if err != nil {
		t.Fatalf("CreateRole failed: %v", err)
	}
	if role.Name != "custom_manager" {
		t.Errorf("expected role name 'custom_manager', got '%s'", role.Name)
	}

	// Update role permissions
	err = svc.UpdateRolePermissions(ctx, service.UpdateRolePermissionsInput{
		RoleID:        role.ID,
		PermissionIDs: []string{"p1", "p2"},
	})
	if err != nil {
		t.Fatalf("UpdateRolePermissions failed: %v", err)
	}

	// Assign role to user
	err = svc.AssignUserRole(ctx, service.AssignUserRoleInput{
		UserID:   "usr_999",
		TenantID: tenantID,
		RoleID:   role.ID,
	})
	if err != nil {
		t.Fatalf("AssignUserRole failed: %v", err)
	}

	// Fetch user role
	ur, err := svc.GetUserRole(ctx, "usr_999", tenantID)
	if err != nil {
		t.Fatalf("GetUserRole failed: %v", err)
	}
	if ur.RoleID != role.ID {
		t.Errorf("expected role ID '%s', got '%s'", role.ID, ur.RoleID)
	}
}

func TestPermissionService_SeedDefaultRoles(t *testing.T) {
	pRepo := &mockPermRepo{}
	rRepo := newMockRoleRepo()
	svc := service.NewPermissionService(pRepo, rRepo)
	ctx := context.Background()

	_ = pRepo.BulkUpsertPermissions(ctx, "order-service", []repository.RegisterPermissionItem{
		{Name: "orders:create"},
	})

	err := svc.SeedDefaultRolesForTenant(ctx, "tnt_seed", "usr_admin")
	if err != nil {
		t.Fatalf("SeedDefaultRolesForTenant failed: %v", err)
	}

	ur, err := svc.GetUserRole(ctx, "usr_admin", "tnt_seed")
	if err != nil {
		t.Fatalf("expected admin user role to be seeded: %v", err)
	}
	if ur.Role == nil || ur.Role.Name != "admin" {
		t.Errorf("expected assigned role name 'admin', got '%v'", ur.Role)
	}
}
