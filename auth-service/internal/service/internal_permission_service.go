package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"auth-service/internal/domain"
	"auth-service/internal/repository"
)

type PermissionRepository interface {
	BulkUpsertPermissions(ctx context.Context, serviceName string, items []repository.RegisterPermissionItem) error
	ListAllPermissions(ctx context.Context) ([]domain.Permission, error)
}

type PermissionOutput struct {
	ID          string
	Name        string
	Service     string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func toPermissionOutput(p domain.Permission) PermissionOutput {
	return PermissionOutput{
		ID:          p.ID,
		Name:        p.Name,
		Service:     p.Service,
		Description: p.Description,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
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

func (internalPermissionService *InternalPermissionService) RegisterPermissions(ctx context.Context, input InternalRegisterPermissionsInput) error {
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

	if err := internalPermissionService.permissionRepository.BulkUpsertPermissions(ctx, input.Service, items); err != nil {
		return fmt.Errorf("internal permission service: failed to register permissions: %w", err)
	}

	log.Printf("InternalPermissionService: Registered %d permissions for service '%s'", len(input.Permissions), input.Service)
	return nil
}

func (internalPermissionService *InternalPermissionService) ListPermissions(ctx context.Context) ([]PermissionOutput, error) {
	permissions, err := internalPermissionService.permissionRepository.ListAllPermissions(ctx)
	if err != nil {
		return nil, err
	}
	outputs := make([]PermissionOutput, len(permissions))
	for i, p := range permissions {
		outputs[i] = toPermissionOutput(p)
	}
	return outputs, nil
}

func (internalPermissionService *InternalPermissionService) GetUserPermissionVersion(ctx context.Context, userID, tenantID string) (int64, error) {
	if userID == "" {
		return 1, domain.ErrUserIDRequired
	}
	if tenantID == "" {
		return 1, domain.ErrTenantIDRequired
	}
	return internalPermissionService.roleRepository.GetUserPermissionVersion(ctx, userID, tenantID)
}

func (internalPermissionService *InternalPermissionService) SeedDefaultRolesForTenant(ctx context.Context, tenantID string, adminUserID string) error {
	if tenantID == "" {
		return domain.ErrTenantIDRequired
	}

	adminRole, err := internalPermissionService.roleRepository.FindRoleByName(ctx, &tenantID, "admin")
	if err != nil {
		createdAdmin, createErr := internalPermissionService.roleRepository.CreateRole(ctx, repository.CreateRoleInput{
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

	allPerms, err := internalPermissionService.permissionRepository.ListAllPermissions(ctx)
	if err == nil && len(allPerms) > 0 && adminRole != nil {
		permIDs := make([]string, len(allPerms))
		for i, p := range allPerms {
			permIDs[i] = p.ID
		}
		_ = internalPermissionService.roleRepository.UpdateRolePermissions(ctx, adminRole.ID, permIDs)
	}

	_, _ = internalPermissionService.roleRepository.CreateRole(ctx, repository.CreateRoleInput{
		TenantID:    &tenantID,
		Name:        "viewer",
		Description: "Read-only tenant access",
		IsSystem:    true,
	})

	if adminUserID != "" && adminRole != nil {
		if err := internalPermissionService.roleRepository.AssignUserRole(ctx, adminUserID, tenantID, adminRole.ID, nil); err != nil {
			return fmt.Errorf("internal permission service: failed to assign admin role to registering user: %w", err)
		}
	}

	log.Printf("InternalPermissionService: Seeded default roles for tenant '%s' (Admin user: '%s')", tenantID, adminUserID)
	return nil
}
