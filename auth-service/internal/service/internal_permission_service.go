package service

import (
	"context"
	"errors"
	"fmt"
	"log"

	"auth-service/internal/domain"
	"auth-service/internal/repository"
)

type PermissionRepository interface {
	BulkUpsertPermissions(ctx context.Context, serviceName string, items []repository.RegisterPermissionItem) error
	ListAllPermissions(ctx context.Context) ([]domain.Permission, error)
}

type InternalRegisterPermissionItem struct {
	Name        string
	Description string
}

type InternalRegisterPermissionsInput struct {
	Service     string
	Permissions []InternalRegisterPermissionItem
}

type InternalPermissionService struct {
	permissionRepository PermissionRepository
	roleRepository       RoleRepository
}

func NewInternalPermissionService(permissionRepository PermissionRepository, roleRepository RoleRepository) *InternalPermissionService {
	return &InternalPermissionService{
		permissionRepository: permissionRepository,
		roleRepository:       roleRepository,
	}
}

func (s *InternalPermissionService) RegisterPermissions(ctx context.Context, input InternalRegisterPermissionsInput) error {
	if input.Service == "" {
		return errors.New("service name is required")
	}
	if len(input.Permissions) == 0 {
		return nil
	}

	items := make([]repository.RegisterPermissionItem, len(input.Permissions))
	for i, item := range input.Permissions {
		items[i] = repository.RegisterPermissionItem{
			Name:        item.Name,
			Description: item.Description,
		}
	}

	if err := s.permissionRepository.BulkUpsertPermissions(ctx, input.Service, items); err != nil {
		return fmt.Errorf("internal permission service: failed to register permissions: %w", err)
	}

	log.Printf("InternalPermissionService: Registered %d permissions for service '%s'", len(input.Permissions), input.Service)
	return nil
}

func (s *InternalPermissionService) ListPermissions(ctx context.Context) ([]domain.Permission, error) {
	return s.permissionRepository.ListAllPermissions(ctx)
}

func (s *InternalPermissionService) GetUserPermissionVersion(ctx context.Context, userID, tenantID string) (int64, error) {
	if userID == "" {
		return 1, domain.ErrUserIDRequired
	}
	if tenantID == "" {
		return 1, domain.ErrTenantIDRequired
	}
	return s.roleRepository.GetUserPermissionVersion(ctx, userID, tenantID)
}

// SeedDefaultRolesForTenant creates default system roles (admin, editor, viewer) for a tenant
// and assigns adminUserID the "admin" role.
func (s *InternalPermissionService) SeedDefaultRolesForTenant(ctx context.Context, tenantID string, adminUserID string) error {
	if tenantID == "" {
		return domain.ErrTenantIDRequired
	}

	// 1. Ensure admin role exists for this tenant
	adminRole, err := s.roleRepository.FindRoleByName(ctx, &tenantID, "admin")
	if err != nil {
		// create tenant admin role
		createdAdmin, createErr := s.roleRepository.CreateRole(ctx, repository.CreateRoleInput{
			TenantID:    &tenantID,
			Name:        "admin",
			Description: "Full tenant administration access",
			IsSystem:    true,
		})
		if createErr != nil && createErr != domain.ErrRoleAlreadyExists {
			return fmt.Errorf("internal permission service: failed to seed admin role: %w", createErr)
		}
		adminRole = createdAdmin
	}

	// 2. Attach all existing permissions to tenant admin role
	allPerms, err := s.permissionRepository.ListAllPermissions(ctx)
	if err == nil && len(allPerms) > 0 && adminRole != nil {
		permIDs := make([]string, len(allPerms))
		for i, p := range allPerms {
			permIDs[i] = p.ID
		}
		_ = s.roleRepository.UpdateRolePermissions(ctx, adminRole.ID, permIDs)
	}

	// 3. Ensure viewer role exists
	_, _ = s.roleRepository.CreateRole(ctx, repository.CreateRoleInput{
		TenantID:    &tenantID,
		Name:        "viewer",
		Description: "Read-only tenant access",
		IsSystem:    true,
	})

	// 4. Assign adminUserID to admin role if specified
	if adminUserID != "" && adminRole != nil {
		if err := s.roleRepository.AssignUserRole(ctx, adminUserID, tenantID, adminRole.ID, nil); err != nil {
			return fmt.Errorf("internal permission service: failed to assign admin role to registering user: %w", err)
		}
	}

	log.Printf("InternalPermissionService: Seeded default roles for tenant '%s' (Admin user: '%s')", tenantID, adminUserID)
	return nil
}
