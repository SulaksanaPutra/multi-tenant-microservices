package service_test

import (
	"context"
	"testing"

	"auth-service/internal/domain"
	"auth-service/internal/repository"
	"auth-service/internal/service"
)

type mockPermissionRepository struct {
	permissions []domain.Permission
}

func (m *mockPermissionRepository) BulkUpsertPermissions(_ context.Context, serviceName string, items []repository.RegisterPermissionItem) error {
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

func (m *mockPermissionRepository) ListAllPermissions(_ context.Context) ([]domain.Permission, error) {
	return m.permissions, nil
}

func TestInternalPermissionService_RegisterAndList(t *testing.T) {
	permissionRepository := &mockPermissionRepository{}
	roleRepository := newMockRoleRepository()
	internalPermissionService := service.NewInternalPermissionService(permissionRepository, roleRepository)
	ctx := context.Background()

	err := internalPermissionService.RegisterPermissions(ctx, service.InternalRegisterPermissionsInput{
		Service: "order-service",
		Permissions: []service.InternalRegisterPermissionItem{
			{Name: "orders:write", Description: "Create order"},
			{Name: "orders:read", Description: "Read order"},
		},
	})
	if err != nil {
		t.Fatalf("RegisterPermissions failed: %v", err)
	}

	perms, err := internalPermissionService.ListPermissions(ctx)
	if err != nil {
		t.Fatalf("ListPermissions failed: %v", err)
	}
	if len(perms) != 2 {
		t.Fatalf("expected 2 permissions, got %d", len(perms))
	}
}

func TestInternalPermissionService_SeedDefaultRoles(t *testing.T) {
	permissionRepository := &mockPermissionRepository{}
	roleRepository := newMockRoleRepository()
	internalPermissionService := service.NewInternalPermissionService(permissionRepository, roleRepository)
	ctx := context.Background()

	_ = permissionRepository.BulkUpsertPermissions(ctx, "order-service", []repository.RegisterPermissionItem{
		{Name: "orders:write"},
	})

	err := internalPermissionService.SeedDefaultRolesForTenant(ctx, "tnt_seed", "usr_admin")
	if err != nil {
		t.Fatalf("SeedDefaultRolesForTenant failed: %v", err)
	}

	roleService := service.NewRoleService(roleRepository)
	userRoleOutput, err := roleService.GetUserRole(ctx, "usr_admin", "tnt_seed")
	if err != nil {
		t.Fatalf("expected admin user role to be seeded: %v", err)
	}
	if userRoleOutput.Role == nil || userRoleOutput.Role.Name != "admin" {
		t.Errorf("expected assigned role name 'admin', got '%v'", userRoleOutput.Role)
	}
}

func TestInternalPermissionService_GetUserPermissionVersion(t *testing.T) {
	permissionRepository := &mockPermissionRepository{}
	roleRepository := newMockRoleRepository()
	internalPermissionService := service.NewInternalPermissionService(permissionRepository, roleRepository)
	ctx := context.Background()

	ver, err := internalPermissionService.GetUserPermissionVersion(ctx, "usr_100", "tnt_001")
	if err != nil {
		t.Fatalf("GetUserPermissionVersion failed: %v", err)
	}
	if ver != 1 {
		t.Errorf("expected default permission version 1, got %d", ver)
	}
}
