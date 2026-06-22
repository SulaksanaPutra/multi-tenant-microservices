package service

import (
	"context"
	"errors"
	"fmt"
	"log"

	"auth-service/internal/domain"
	"auth-service/internal/repository"
)

type RoleRepository interface {
	CreateRole(ctx context.Context, input repository.CreateRoleInput) (*domain.Role, error)
	FindRoleByID(ctx context.Context, id string) (*domain.Role, error)
	FindRoleByName(ctx context.Context, tenantID *string, name string) (*domain.Role, error)
	FindRolesByTenantID(ctx context.Context, tenantID string) ([]domain.Role, error)
	UpdateRolePermissions(ctx context.Context, roleID string, permissionIDs []string) error
	DeleteRole(ctx context.Context, id string) error
	AssignUserRole(ctx context.Context, userID, tenantID, roleID string, assignedBy *string) error
	FindUserRole(ctx context.Context, userID, tenantID string) (*domain.UserRole, error)
	UserHasMembership(ctx context.Context, userID, tenantID string) (bool, error)
	GetUserPermissionVersion(ctx context.Context, userID, tenantID string) (int64, error)
	BumpUserPermissionVersionsForRole(ctx context.Context, roleID string) error
	ListUserRolesByTenant(ctx context.Context, tenantID string, userIDs []string) ([]repository.UserRoleBrief, error)
}

type CreateRoleInput struct {
	TenantID    string
	Name        string
	Description string
}

type UpdateRolePermissionsInput struct {
	RoleID        string
	PermissionIDs []string
}

type AssignUserRoleInput struct {
	UserID     string
	TenantID   string
	RoleID     string
	AssignedBy *string
}

type UserRoleAssignmentOutput struct {
	UserID   string
	RoleID   string
	RoleName string
}

type RoleService struct {
	roleRepository RoleRepository
}

func NewRoleService(roleRepository RoleRepository) *RoleService {
	return &RoleService{
		roleRepository: roleRepository,
	}
}

func (s *RoleService) CreateRole(ctx context.Context, input CreateRoleInput) (*domain.Role, error) {
	if input.TenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}
	if input.Name == "" {
		return nil, domain.ErrRoleNameRequired
	}

	roleInput := repository.CreateRoleInput{
		TenantID:    &input.TenantID,
		Name:        input.Name,
		Description: input.Description,
		IsSystem:    false,
	}

	created, err := s.roleRepository.CreateRole(ctx, roleInput)
	if err != nil {
		if errors.Is(err, domain.ErrRoleAlreadyExists) {
			return s.roleRepository.FindRoleByName(ctx, &input.TenantID, input.Name)
		}
		return nil, err
	}

	log.Printf("RoleService: Created role '%s' (ID: %s) for tenant '%s'", created.Name, created.ID, input.TenantID)
	return created, nil
}

func (s *RoleService) GetRole(ctx context.Context, roleID string) (*domain.Role, error) {
	if roleID == "" {
		return nil, domain.ErrRoleIDRequired
	}
	return s.roleRepository.FindRoleByID(ctx, roleID)
}

func (s *RoleService) ListRolesForTenant(ctx context.Context, tenantID string) ([]domain.Role, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}
	return s.roleRepository.FindRolesByTenantID(ctx, tenantID)
}

func (s *RoleService) UpdateRolePermissions(ctx context.Context, input UpdateRolePermissionsInput) error {
	if input.RoleID == "" {
		return domain.ErrRoleIDRequired
	}

	role, err := s.roleRepository.FindRoleByID(ctx, input.RoleID)
	if err != nil {
		return err
	}

	if role.IsSystem {
		return domain.ErrSystemRoleProtected
	}

	if err := s.roleRepository.UpdateRolePermissions(ctx, input.RoleID, input.PermissionIDs); err != nil {
		return fmt.Errorf("role service: failed to update role permissions: %w", err)
	}

	// Invalidate versions for users assigned to this role
	if err := s.roleRepository.BumpUserPermissionVersionsForRole(ctx, input.RoleID); err != nil {
		log.Printf("RoleService: Warning — failed to bump user permission versions for role '%s': %v", input.RoleID, err)
	}

	log.Printf("RoleService: Updated permissions for role '%s' (ID: %s)", role.Name, role.ID)
	return nil
}

func (s *RoleService) DeleteRole(ctx context.Context, roleID string) error {
	if roleID == "" {
		return domain.ErrRoleIDRequired
	}
	return s.roleRepository.DeleteRole(ctx, roleID)
}

func (s *RoleService) AssignUserRole(ctx context.Context, input AssignUserRoleInput) error {
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
	if _, err := s.roleRepository.FindRoleByID(ctx, input.RoleID); err != nil {
		return err
	}

	// Verify the target user is an actual member of the tenant to prevent cross-tenant role assignment
	isMember, err := s.roleRepository.UserHasMembership(ctx, input.UserID, input.TenantID)
	if err != nil {
		return fmt.Errorf("role service: failed to verify tenant membership: %w", err)
	}
	if !isMember {
		return domain.ErrTenantMembershipNotFound
	}

	if err := s.roleRepository.AssignUserRole(ctx, input.UserID, input.TenantID, input.RoleID, input.AssignedBy); err != nil {
		return fmt.Errorf("role service: failed to assign user role: %w", err)
	}

	log.Printf("RoleService: Assigned role '%s' to user '%s' for tenant '%s'", input.RoleID, input.UserID, input.TenantID)
	return nil
}

func (s *RoleService) GetUserRole(ctx context.Context, userID, tenantID string) (*domain.UserRole, error) {
	if userID == "" {
		return nil, domain.ErrUserIDRequired
	}
	if tenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}

	isMember, err := s.roleRepository.UserHasMembership(ctx, userID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("role service: failed to verify tenant membership: %w", err)
	}
	if !isMember {
		return nil, domain.ErrTenantMembershipNotFound
	}

	return s.roleRepository.FindUserRole(ctx, userID, tenantID)
}

func (s *RoleService) ListUserRolesForTenant(ctx context.Context, tenantID string, userIDs []string) ([]UserRoleAssignmentOutput, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}
	if len(userIDs) == 0 {
		return []UserRoleAssignmentOutput{}, nil
	}

	briefs, err := s.roleRepository.ListUserRolesByTenant(ctx, tenantID, userIDs)
	if err != nil {
		return nil, err
	}

	outputs := make([]UserRoleAssignmentOutput, len(briefs))
	for i, brief := range briefs {
		outputs[i] = UserRoleAssignmentOutput{
			UserID:   brief.UserID,
			RoleID:   brief.RoleID,
			RoleName: brief.RoleName,
		}
	}
	return outputs, nil
}
