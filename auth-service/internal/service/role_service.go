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

type RoleOutput struct {
	ID          string
	TenantID    *string
	Name        string
	Description string
	IsSystem    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Permissions []PermissionOutput
}

type UserRoleOutput struct {
	UserID     string
	TenantID   string
	RoleID     string
	AssignedAt time.Time
	AssignedBy *string
	Role       *RoleOutput
}

func toRoleOutput(r domain.Role) RoleOutput {
	permissions := make([]PermissionOutput, len(r.Permissions))
	for i, p := range r.Permissions {
		permissions[i] = toPermissionOutput(p)
	}
	return RoleOutput{
		ID:          r.ID,
		TenantID:    r.TenantID,
		Name:        r.Name,
		Description: r.Description,
		IsSystem:    r.IsSystem,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
		Permissions: permissions,
	}
}

func toUserRoleOutput(ur domain.UserRole) UserRoleOutput {
	var role *RoleOutput
	if ur.Role != nil {
		roleOutput := toRoleOutput(*ur.Role)
		role = &roleOutput
	}
	return UserRoleOutput{
		UserID:     ur.UserID,
		TenantID:   ur.TenantID,
		RoleID:     ur.RoleID,
		AssignedAt: ur.AssignedAt,
		AssignedBy: ur.AssignedBy,
		Role:       role,
	}
}

type RoleService struct {
	roleRepository RoleRepository
}

func NewRoleService(roleRepository RoleRepository) *RoleService {
	return &RoleService{
		roleRepository: roleRepository,
	}
}

func (roleService *RoleService) CreateRole(ctx context.Context, input CreateRoleInput) (*RoleOutput, error) {
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

	created, err := roleService.roleRepository.CreateRole(ctx, roleInput)
	if err != nil {
		if errors.Is(err, domain.ErrRoleAlreadyExists) {
			existing, findErr := roleService.roleRepository.FindRoleByName(ctx, &input.TenantID, input.Name)
			if findErr != nil {
				return nil, findErr
			}
			if existing == nil {
				return nil, fmt.Errorf("role service: failed to fetch existing role '%s': %w", input.Name, domain.ErrRoleNotFound)
			}
			output := toRoleOutput(*existing)
			return &output, nil
		}
		return nil, err
	}

	log.Printf("RoleService: Created role '%s' (ID: %s) for tenant '%s'", created.Name, created.ID, input.TenantID)
	output := toRoleOutput(*created)
	return &output, nil
}

func (roleService *RoleService) GetRole(ctx context.Context, roleID string) (*RoleOutput, error) {
	if roleID == "" {
		return nil, domain.ErrRoleIDRequired
	}
	role, err := roleService.roleRepository.FindRoleByID(ctx, roleID)
	if err != nil {
		return nil, err
	}
	output := toRoleOutput(*role)
	return &output, nil
}

func (roleService *RoleService) ListRolesForTenant(ctx context.Context, tenantID string) ([]RoleOutput, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}
	roles, err := roleService.roleRepository.FindRolesByTenantID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	outputs := make([]RoleOutput, len(roles))
	for i, r := range roles {
		outputs[i] = toRoleOutput(r)
	}
	return outputs, nil
}

func (roleService *RoleService) UpdateRolePermissions(ctx context.Context, input UpdateRolePermissionsInput) error {
	if input.RoleID == "" {
		return domain.ErrRoleIDRequired
	}

	role, err := roleService.roleRepository.FindRoleByID(ctx, input.RoleID)
	if err != nil {
		return err
	}

	if role.IsSystem {
		return domain.ErrSystemRoleProtected
	}

	if err := roleService.roleRepository.UpdateRolePermissions(ctx, input.RoleID, input.PermissionIDs); err != nil {
		return fmt.Errorf("role service: failed to update role permissions: %w", err)
	}

	if err := roleService.roleRepository.BumpUserPermissionVersionsForRole(ctx, input.RoleID); err != nil {
		log.Printf("RoleService: Warning — failed to bump user permission versions for role '%s': %v", input.RoleID, err)
	}

	log.Printf("RoleService: Updated permissions for role '%s' (ID: %s)", role.Name, role.ID)
	return nil
}

func (roleService *RoleService) DeleteRole(ctx context.Context, roleID string) error {
	if roleID == "" {
		return domain.ErrRoleIDRequired
	}
	return roleService.roleRepository.DeleteRole(ctx, roleID)
}

func (roleService *RoleService) AssignUserRole(ctx context.Context, input AssignUserRoleInput) error {
	if input.UserID == "" {
		return domain.ErrUserIDRequired
	}
	if input.TenantID == "" {
		return domain.ErrTenantIDRequired
	}
	if input.RoleID == "" {
		return domain.ErrRoleIDRequired
	}

	if _, err := roleService.roleRepository.FindRoleByID(ctx, input.RoleID); err != nil {
		return err
	}

	isMember, err := roleService.roleRepository.UserHasMembership(ctx, input.UserID, input.TenantID)
	if err != nil {
		return fmt.Errorf("role service: failed to verify tenant membership: %w", err)
	}
	if !isMember {
		return domain.ErrTenantMembershipNotFound
	}

	if err := roleService.roleRepository.AssignUserRole(ctx, input.UserID, input.TenantID, input.RoleID, input.AssignedBy); err != nil {
		return fmt.Errorf("role service: failed to assign user role: %w", err)
	}

	log.Printf("RoleService: Assigned role '%s' to user '%s' for tenant '%s'", input.RoleID, input.UserID, input.TenantID)
	return nil
}

func (roleService *RoleService) GetUserRole(ctx context.Context, userID, tenantID string) (*UserRoleOutput, error) {
	if userID == "" {
		return nil, domain.ErrUserIDRequired
	}
	if tenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}

	isMember, err := roleService.roleRepository.UserHasMembership(ctx, userID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("role service: failed to verify tenant membership: %w", err)
	}
	if !isMember {
		return nil, domain.ErrTenantMembershipNotFound
	}

	userRole, err := roleService.roleRepository.FindUserRole(ctx, userID, tenantID)
	if err != nil {
		return nil, err
	}
	if userRole == nil {
		return nil, fmt.Errorf("role service: no role found for user '%s' in tenant '%s': %w", userID, tenantID, domain.ErrRoleNotFound)
	}
	output := toUserRoleOutput(*userRole)
	return &output, nil
}

func (roleService *RoleService) ListUserRolesForTenant(ctx context.Context, tenantID string, userIDs []string) ([]UserRoleAssignmentOutput, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}
	if len(userIDs) == 0 {
		return []UserRoleAssignmentOutput{}, nil
	}

	briefs, err := roleService.roleRepository.ListUserRolesByTenant(ctx, tenantID, userIDs)
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
