package handler

import (
	"context"

	"auth-service/internal/service"
)

// =============================================================================
// Public Domain Service Contracts
// =============================================================================

// AuthService is the handler-side interface for user authentication and session workflows.
type AuthService interface {
	SetupPassword(ctx context.Context, input service.SetupPasswordInput) (*service.TokenPair, error)
	Login(ctx context.Context, input service.LoginInput) (*service.LoginOutput, error)
	SelectWorkspace(ctx context.Context, input service.SelectWorkspaceInput) (*service.TokenPair, error)
	RefreshToken(ctx context.Context, input service.RefreshTokenInput) (*service.TokenPair, error)
	Logout(ctx context.Context, input service.LogoutInput) error
}

// RoleService is the handler-side interface for RBAC role and permission management.
type RoleService interface {
	CreateRole(ctx context.Context, input service.CreateRoleInput) (*service.RoleOutput, error)
	GetRole(ctx context.Context, roleID string) (*service.RoleOutput, error)
	ListRolesForTenant(ctx context.Context, tenantID string) ([]service.RoleOutput, error)
	UpdateRolePermissions(ctx context.Context, input service.UpdateRolePermissionsInput) error
	DeleteRole(ctx context.Context, roleID string) error
	AssignUserRole(ctx context.Context, input service.AssignUserRoleInput) error
	GetUserRole(ctx context.Context, userID, tenantID string) (*service.UserRoleOutput, error)
	ListUserRolesForTenant(ctx context.Context, tenantID string, userIDs []string) ([]service.UserRoleAssignmentOutput, error)
}

// PermissionService is the handler-side interface for permission catalog queries.
type PermissionService interface {
	ListPermissions(ctx context.Context) ([]service.PermissionOutput, error)
}

// =============================================================================
// Internal Inter-Service Contracts
// =============================================================================

// InternalAuthAppService is the handler-side interface for inter-service token operations.
type InternalAuthAppService interface {
	CreatePasswordSetupToken(ctx context.Context, input service.InternalCreateSetupTokenInput) (string, error)
}

// InternalPermissionService is the handler-side interface for inter-service permission registration.
type InternalPermissionService interface {
	RegisterPermissions(ctx context.Context, input service.InternalRegisterPermissionsInput) error
	GetUserPermissionVersion(ctx context.Context, userID, tenantID string) (int64, error)
}
