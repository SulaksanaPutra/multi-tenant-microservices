package domain

import "errors"

var (
	ErrInvalidCredentials = errors.New("auth service: invalid email or password")
	ErrCredentialNotFound = errors.New("auth service: credential not found")
	ErrTokenExpired       = errors.New("auth service: token has expired")
	ErrTokenRevoked       = errors.New("auth service: token has been revoked")
	ErrTokenAlreadyUsed   = errors.New("auth service: setup token has already been used")
	ErrTokenNotFound      = errors.New("auth service: token not found")
	ErrEmailRequired      = errors.New("auth service: email is required")
	ErrPasswordRequired   = errors.New("auth service: password is required")
	ErrUserIDRequired     = errors.New("auth service: user_id is required")

	ErrPermissionNotFound  = errors.New("auth service: permission not found")
	ErrRoleNotFound        = errors.New("auth service: role not found")
	ErrRoleAlreadyExists   = errors.New("auth service: role already exists for tenant")
	ErrSystemRoleProtected = errors.New("auth service: system roles cannot be modified or deleted")
	ErrRoleIDRequired      = errors.New("auth service: role_id is required")
	ErrTenantIDRequired    = errors.New("auth service: tenant_id is required")
	ErrRoleNameRequired    = errors.New("auth service: role name is required")
)
