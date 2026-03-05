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

func TestInternalPermissionService_RegisterAndList(t *testing.T) {
	pRepo := &mockPermRepo{}
	rRepo := newMockRoleRepo()
	svc := service.NewInternalPermissionService(pRepo, rRepo)
	ctx := context.Background()

	err := svc.RegisterPermissions(ctx, service.InternalRegisterPermissionsInput{
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

func TestInternalPermissionService_SeedDefaultRoles(t *testing.T) {
	pRepo := &mockPermRepo{}
	rRepo := newMockRoleRepo()
	svc := service.NewInternalPermissionService(pRepo, rRepo)
	ctx := context.Background()

	_ = pRepo.BulkUpsertPermissions(ctx, "order-service", []repository.RegisterPermissionItem{
		{Name: "orders:create"},
	})

	err := svc.SeedDefaultRolesForTenant(ctx, "tnt_seed", "usr_admin")
	if err != nil {
		t.Fatalf("SeedDefaultRolesForTenant failed: %v", err)
	}

	rSvc := service.NewRoleService(rRepo)
	ur, err := rSvc.GetUserRole(ctx, "usr_admin", "tnt_seed")
	if err != nil {
		t.Fatalf("expected admin user role to be seeded: %v", err)
	}
	if ur.Role == nil || ur.Role.Name != "admin" {
		t.Errorf("expected assigned role name 'admin', got '%v'", ur.Role)
	}
}

func TestInternalPermissionService_GetUserPermissionVersion(t *testing.T) {
	pRepo := &mockPermRepo{}
	rRepo := newMockRoleRepo()
	svc := service.NewInternalPermissionService(pRepo, rRepo)
	ctx := context.Background()

	ver, err := svc.GetUserPermissionVersion(ctx, "usr_100", "tnt_001")
	if err != nil {
		t.Fatalf("GetUserPermissionVersion failed: %v", err)
	}
	if ver != 1 {
		t.Errorf("expected default permission version 1, got %d", ver)
	}
}
