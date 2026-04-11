package service_test

import (
	"context"
	"errors"
	"testing"

	"auth-service/internal/domain"
	"auth-service/internal/repository"
	"auth-service/internal/service"
)

type mockRoleRepo struct {
	roles       map[string]*domain.Role
	userRoles   map[string]*domain.UserRole
	versions    map[string]int64
	memberships map[string]bool
}

func newMockRoleRepo() *mockRoleRepo {
	return &mockRoleRepo{
		roles:       make(map[string]*domain.Role),
		userRoles:   make(map[string]*domain.UserRole),
		versions:    make(map[string]int64),
		memberships: make(map[string]bool),
	}
}

func (m *mockRoleRepo) UserHasMembership(_ context.Context, userID, tenantID string) (bool, error) {
	if v, ok := m.memberships[userID+"_"+tenantID]; ok {
		return v, nil
	}
	return true, nil
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

func (m *mockRoleRepo) ListUserRolesByTenant(_ context.Context, tenantID string, userIDs []string) ([]repository.UserRoleBrief, error) {
	var briefs []repository.UserRoleBrief
	for _, uid := range userIDs {
		ur := m.userRoles[uid+"_"+tenantID]
		if ur == nil {
			continue
		}
		role := m.roles[ur.RoleID]
		var name string
		if role != nil {
			name = role.Name
		}
		briefs = append(briefs, repository.UserRoleBrief{
			UserID:   ur.UserID,
			RoleID:   ur.RoleID,
			RoleName: name,
		})
	}
	return briefs, nil
}

func TestRoleService_AssignUserRole_RejectsNonMember(t *testing.T) {
	rRepo := newMockRoleRepo()
	svc := service.NewRoleService(rRepo)
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

	rRepo.memberships["usr_999"+"_"+tenantID] = false

	err = svc.AssignUserRole(ctx, service.AssignUserRoleInput{
		UserID:   "usr_999",
		TenantID: tenantID,
		RoleID:   role.ID,
	})
	if !errors.Is(err, domain.ErrTenantMembershipNotFound) {
		t.Fatalf("expected ErrTenantMembershipNotFound, got %v", err)
	}

	if _, err := svc.GetUserRole(ctx, "usr_999", tenantID); !errors.Is(err, domain.ErrTenantMembershipNotFound) {
		t.Fatalf("expected GetUserRole to reject non-member, got %v", err)
	}
}

func TestRoleService_CreateAndManageRole(t *testing.T) {
	rRepo := newMockRoleRepo()
	svc := service.NewRoleService(rRepo)
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

	// Fetch role by ID
	fetched, err := svc.GetRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("GetRole failed: %v", err)
	}
	if fetched.ID != role.ID {
		t.Errorf("expected role ID '%s', got '%s'", role.ID, fetched.ID)
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

	// List roles for tenant
	roles, err := svc.ListRolesForTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("ListRolesForTenant failed: %v", err)
	}
	if len(roles) != 1 {
		t.Errorf("expected 1 role for tenant, got %d", len(roles))
	}

	// Delete role
	err = svc.DeleteRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("DeleteRole failed: %v", err)
	}
}
