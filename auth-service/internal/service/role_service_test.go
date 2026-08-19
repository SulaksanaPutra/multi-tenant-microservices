package service_test

import (
	"context"
	"errors"
	"testing"

	"auth-service/internal/domain"
	"auth-service/internal/repository"
	"auth-service/internal/service"
)

type mockRoleRepository struct {
	roles       map[string]*domain.Role
	userRoles   map[string]*domain.UserRole
	versions    map[string]int64
	memberships map[string]bool
}

func newMockRoleRepository() *mockRoleRepository {
	return &mockRoleRepository{
		roles:       make(map[string]*domain.Role),
		userRoles:   make(map[string]*domain.UserRole),
		versions:    make(map[string]int64),
		memberships: make(map[string]bool),
	}
}

func (m *mockRoleRepository) UserHasMembership(_ context.Context, userID, tenantID string) (bool, error) {
	if v, ok := m.memberships[userID+"_"+tenantID]; ok {
		return v, nil
	}
	return true, nil
}

func (m *mockRoleRepository) CreateRole(_ context.Context, input repository.CreateRoleInput) (*domain.Role, error) {
	r := domain.Role{
		TenantID:    input.TenantID,
		Name:        input.Name,
		Description: input.Description,
		IsSystem:    input.IsSystem,
	}
	r.ID = "role_" + input.Name
	m.roles[r.ID] = &r
	return &r, nil
}

func (m *mockRoleRepository) FindRoleByID(_ context.Context, id string) (*domain.Role, error) {
	if r, ok := m.roles[id]; ok {
		return r, nil
	}
	return nil, domain.ErrRoleNotFound
}

func (m *mockRoleRepository) FindRoleByName(_ context.Context, tenantID *string, name string) (*domain.Role, error) {
	for _, r := range m.roles {
		if r.Name == name {
			return r, nil
		}
	}
	return nil, domain.ErrRoleNotFound
}

func (m *mockRoleRepository) FindRolesByTenantID(_ context.Context, tenantID string) ([]domain.Role, error) {
	var res []domain.Role
	for _, r := range m.roles {
		if r.TenantID != nil && *r.TenantID == tenantID {
			res = append(res, *r)
		}
	}
	return res, nil
}

func (m *mockRoleRepository) UpdateRolePermissions(_ context.Context, roleID string, permissionIDs []string) error {
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

func (m *mockRoleRepository) DeleteRole(_ context.Context, id string) error {
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

func (m *mockRoleRepository) AssignUserRole(_ context.Context, userID, tenantID, roleID string, assignedBy *string) error {
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

func (m *mockRoleRepository) FindUserRole(_ context.Context, userID, tenantID string) (*domain.UserRole, error) {
	key := userID + "_" + tenantID
	if userRole, ok := m.userRoles[key]; ok {
		return userRole, nil
	}
	return nil, domain.ErrRoleNotFound
}

func (m *mockRoleRepository) GetUserPermissionVersion(_ context.Context, userID, tenantID string) (int64, error) {
	key := userID + "_" + tenantID
	ver, ok := m.versions[key]
	if !ok {
		return 1, nil
	}
	return ver, nil
}

func (m *mockRoleRepository) BumpUserPermissionVersionsForRole(_ context.Context, roleID string) error {
	for k, userRole := range m.userRoles {
		if userRole.RoleID == roleID {
			m.versions[k]++
		}
	}
	return nil
}

func (m *mockRoleRepository) ListUserRolesByTenant(_ context.Context, tenantID string, userIDs []string) ([]domain.UserRoleAssignment, error) {
	var assignments []domain.UserRoleAssignment
	for _, uid := range userIDs {
		userRole := m.userRoles[uid+"_"+tenantID]
		if userRole == nil {
			continue
		}
		role := m.roles[userRole.RoleID]
		var name string
		if role != nil {
			name = role.Name
		}
		assignments = append(assignments, domain.UserRoleAssignment{
			UserID:   userRole.UserID,
			RoleID:   userRole.RoleID,
			RoleName: name,
		})
	}
	return assignments, nil
}

func TestRoleService_AssignUserRole_RejectsNonMember(t *testing.T) {
	roleRepository := newMockRoleRepository()
	roleService := service.NewRoleService(roleRepository)
	ctx := context.Background()

	tenantID := "tnt_001"
	role, err := roleService.CreateRole(ctx, service.CreateRoleInput{
		TenantID:    tenantID,
		Name:        "custom_manager",
		Description: "Custom Manager Role",
	})
	if err != nil {
		t.Fatalf("CreateRole failed: %v", err)
	}

	roleRepository.memberships["usr_999"+"_"+tenantID] = false

	err = roleService.AssignUserRole(ctx, service.AssignUserRoleInput{
		UserID:   "usr_999",
		TenantID: tenantID,
		RoleID:   role.ID,
	})
	if !errors.Is(err, domain.ErrTenantMembershipNotFound) {
		t.Fatalf("expected ErrTenantMembershipNotFound, got %v", err)
	}

	if _, err := roleService.GetUserRole(ctx, "usr_999", tenantID); !errors.Is(err, domain.ErrTenantMembershipNotFound) {
		t.Fatalf("expected GetUserRole to reject non-member, got %v", err)
	}
}

func TestRoleService_CreateAndManageRole(t *testing.T) {
	roleRepository := newMockRoleRepository()
	roleService := service.NewRoleService(roleRepository)
	ctx := context.Background()

	tenantID := "tnt_001"
	role, err := roleService.CreateRole(ctx, service.CreateRoleInput{
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

	fetched, err := roleService.GetRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("GetRole failed: %v", err)
	}
	if fetched.ID != role.ID {
		t.Errorf("expected role ID '%s', got '%s'", role.ID, fetched.ID)
	}

	err = roleService.UpdateRolePermissions(ctx, service.UpdateRolePermissionsInput{
		RoleID:        role.ID,
		PermissionIDs: []string{"p1", "p2"},
	})
	if err != nil {
		t.Fatalf("UpdateRolePermissions failed: %v", err)
	}

	err = roleService.AssignUserRole(ctx, service.AssignUserRoleInput{
		UserID:   "usr_999",
		TenantID: tenantID,
		RoleID:   role.ID,
	})
	if err != nil {
		t.Fatalf("AssignUserRole failed: %v", err)
	}

	userRole, err := roleService.GetUserRole(ctx, "usr_999", tenantID)
	if err != nil {
		t.Fatalf("GetUserRole failed: %v", err)
	}
	if userRole.RoleID != role.ID {
		t.Errorf("expected role ID '%s', got '%s'", role.ID, userRole.RoleID)
	}

	roles, err := roleService.ListRolesForTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("ListRolesForTenant failed: %v", err)
	}
	if len(roles) != 1 {
		t.Errorf("expected 1 role for tenant, got %d", len(roles))
	}

	err = roleService.DeleteRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("DeleteRole failed: %v", err)
	}
}
