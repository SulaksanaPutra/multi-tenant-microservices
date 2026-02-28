package service

import (
	"context"
	"fmt"
	"log"

	"auth-service/internal/domain"
	"auth-service/internal/repository"
)

type PermissionRepository interface {
	BulkUpsertPermissions(ctx context.Context, serviceName string, items []repository.RegisterPermissionItem) error
	ListAllPermissions(ctx context.Context) ([]domain.Permission, error)
	FindByIDs(ctx context.Context, ids []string) ([]domain.Permission, error)
	FindByName(ctx context.Context, name string) (*domain.Permission, error)
}

type RoleRepository interface {
	CreateRole(ctx context.Context, role domain.Role) (*domain.Role, error)
	FindRoleByID(ctx context.Context, id string) (*domain.Role, error)
	FindRoleByName(ctx context.Context, tenantID *string, name string) (*domain.Role, error)
	FindRolesByTenantID(ctx context.Context, tenantID string) ([]domain.Role, error)
	UpdateRolePermissions(ctx context.Context, roleID string, permissionIDs []string) error
	DeleteRole(ctx context.Context, id string) error
	AssignUserRole(ctx context.Context, userID, tenantID, roleID string, assignedBy *string) error
	FindUserRole(ctx context.Context, userID, tenantID string) (*domain.UserRole, error)
	FindUserPermissions(ctx context.Context, userID, tenantID string) ([]string, int64, error)
	GetUserPermissionVersion(ctx context.Context, userID, tenantID string) (int64, error)
	BumpUserPermissionVersionsForRole(ctx context.Context, roleID string) error
}

type RegisterPermissionsInput struct {
	Service     string                               `json:"service"`
	Permissions []repository.RegisterPermissionItem `json:"permissions"`
}

type CreateRoleInput struct {
	TenantID    string `json:"tenant_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type UpdateRolePermissionsInput struct {
	RoleID        string   `json:"role_id"`
	PermissionIDs []string `json:"permission_ids"`
}

type AssignUserRoleInput struct {
	UserID     string  `json:"user_id"`
	TenantID   string  `json:"tenant_id"`
	RoleID     string  `json:"role_id"`
	AssignedBy *string `json:"assigned_by"`
}

type PermissionService struct {
	permRepo PermissionRepository
	roleRepo RoleRepository
}

func NewPermissionService(permRepo PermissionRepository, roleRepo RoleRepository) *PermissionService {
	return &PermissionService{
		permRepo: permRepo,
		roleRepo: roleRepo,
	}
}

func (s *PermissionService) RegisterPermissions(ctx context.Context, input RegisterPermissionsInput) error {
	if input.Service == "" {
		return fmt.Errorf("service name is required")
	}
	if len(input.Permissions) == 0 {
		return nil
	}

	if err := s.permRepo.BulkUpsertPermissions(ctx, input.Service, input.Permissions); err != nil {
		return fmt.Errorf("permission service: failed to register permissions: %w", err)
	}

	log.Printf("PermissionService: Registered %d permissions for service '%s'", len(input.Permissions), input.Service)
	return nil
}

func (s *PermissionService) ListPermissions(ctx context.Context) ([]domain.Permission, error) {
	return s.permRepo.ListAllPermissions(ctx)
}

func (s *PermissionService) CreateRole(ctx context.Context, input CreateRoleInput) (*domain.Role, error) {
	if input.TenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}
	if input.Name == "" {
		return nil, domain.ErrRoleNameRequired
	}

	role := domain.Role{
		TenantID:    &input.TenantID,
		Name:        input.Name,
		Description: input.Description,
		IsSystem:    false,
	}

	created, err := s.roleRepo.CreateRole(ctx, role)
	if err != nil {
		return nil, err
	}

	log.Printf("PermissionService: Created role '%s' (ID: %s) for tenant '%s'", created.Name, created.ID, input.TenantID)
	return created, nil
}

func (s *PermissionService) GetRole(ctx context.Context, roleID string) (*domain.Role, error) {
	if roleID == "" {
		return nil, domain.ErrRoleIDRequired
	}
	return s.roleRepo.FindRoleByID(ctx, roleID)
}

func (s *PermissionService) ListRolesForTenant(ctx context.Context, tenantID string) ([]domain.Role, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}
	return s.roleRepo.FindRolesByTenantID(ctx, tenantID)
}

func (s *PermissionService) UpdateRolePermissions(ctx context.Context, input UpdateRolePermissionsInput) error {
	if input.RoleID == "" {
		return domain.ErrRoleIDRequired
	}

	role, err := s.roleRepo.FindRoleByID(ctx, input.RoleID)
	if err != nil {
		return err
	}

	if role.IsSystem {
		return domain.ErrSystemRoleProtected
	}

	if err := s.roleRepo.UpdateRolePermissions(ctx, input.RoleID, input.PermissionIDs); err != nil {
		return fmt.Errorf("permission service: failed to update role permissions: %w", err)
	}

	// Invalidate versions for users assigned to this role
	if err := s.roleRepo.BumpUserPermissionVersionsForRole(ctx, input.RoleID); err != nil {
		log.Printf("PermissionService: Warning — failed to bump user permission versions for role '%s': %v", input.RoleID, err)
	}

	log.Printf("PermissionService: Updated permissions for role '%s' (ID: %s)", role.Name, role.ID)
	return nil
}

func (s *PermissionService) DeleteRole(ctx context.Context, roleID string) error {
	if roleID == "" {
		return domain.ErrRoleIDRequired
	}
	return s.roleRepo.DeleteRole(ctx, roleID)
}

func (s *PermissionService) AssignUserRole(ctx context.Context, input AssignUserRoleInput) error {
	if input.UserID == "" {
		return domain.ErrUserIDRequired
	}
	if input.TenantID == "" {
		return domain.ErrTenantIDRequired
	}
	if input.RoleID == "" {
		return domain.ErrRoleIDRequired
	}

	// Verify role exists
	if _, err := s.roleRepo.FindRoleByID(ctx, input.RoleID); err != nil {
		return err
	}

	if err := s.roleRepo.AssignUserRole(ctx, input.UserID, input.TenantID, input.RoleID, input.AssignedBy); err != nil {
		return fmt.Errorf("permission service: failed to assign user role: %w", err)
	}

	log.Printf("PermissionService: Assigned role '%s' to user '%s' for tenant '%s'", input.RoleID, input.UserID, input.TenantID)
	return nil
}

func (s *PermissionService) GetUserRole(ctx context.Context, userID, tenantID string) (*domain.UserRole, error) {
	if userID == "" {
		return nil, domain.ErrUserIDRequired
	}
	if tenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}
	return s.roleRepo.FindUserRole(ctx, userID, tenantID)
}

func (s *PermissionService) GetUserPermissionVersion(ctx context.Context, userID, tenantID string) (int64, error) {
	if userID == "" {
		return 1, domain.ErrUserIDRequired
	}
	if tenantID == "" {
		return 1, domain.ErrTenantIDRequired
	}
	return s.roleRepo.GetUserPermissionVersion(ctx, userID, tenantID)
}

// SeedDefaultRolesForTenant creates default system roles (admin, editor, viewer) for a tenant
// and assigns adminUserID the "admin" role.
func (s *PermissionService) SeedDefaultRolesForTenant(ctx context.Context, tenantID string, adminUserID string) error {
	if tenantID == "" {
		return domain.ErrTenantIDRequired
	}

	// 1. Ensure admin role exists for this tenant
	adminRole, err := s.roleRepo.FindRoleByName(ctx, &tenantID, "admin")
	if err != nil {
		// Create tenant admin role
		createdAdmin, createErr := s.roleRepo.CreateRole(ctx, domain.Role{
			TenantID:    &tenantID,
			Name:        "admin",
			Description: "Full tenant administration access",
			IsSystem:    true,
		})
		if createErr != nil && createErr != domain.ErrRoleAlreadyExists {
			return fmt.Errorf("permission service: failed to seed admin role: %w", createErr)
		}
		adminRole = createdAdmin
	}

	// 2. Attach all existing permissions to tenant admin role
	allPerms, err := s.permRepo.ListAllPermissions(ctx)
	if err == nil && len(allPerms) > 0 && adminRole != nil {
		permIDs := make([]string, len(allPerms))
		for i, p := range allPerms {
			permIDs[i] = p.ID
		}
		_ = s.roleRepo.UpdateRolePermissions(ctx, adminRole.ID, permIDs)
	}

	// 3. Ensure viewer role exists
	_, _ = s.roleRepo.CreateRole(ctx, domain.Role{
		TenantID:    &tenantID,
		Name:        "viewer",
		Description: "Read-only tenant access",
		IsSystem:    true,
	})

	// 4. Assign adminUserID to admin role if specified
	if adminUserID != "" && adminRole != nil {
		if err := s.roleRepo.AssignUserRole(ctx, adminUserID, tenantID, adminRole.ID, nil); err != nil {
			return fmt.Errorf("permission service: failed to assign admin role to registering user: %w", err)
		}
	}

	log.Printf("PermissionService: Seeded default roles for tenant '%s' (Admin user: '%s')", tenantID, adminUserID)
	return nil
}
