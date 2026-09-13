package domain

import "time"

type Permission struct {
	ID          string
	Name        string
	Service     string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Role struct {
	ID          string
	TenantID    *string
	Name        string
	Description string
	IsSystem    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Permissions []Permission
}

type UserRole struct {
	UserID     string
	TenantID   string
	RoleID     string
	AssignedAt time.Time
	AssignedBy *string
	Role       *Role
}

type UserRoleAssignment struct {
	UserID   string
	RoleID   string
	RoleName string
}

type UserPermissionVersion struct {
	UserID    string
	TenantID  string
	Version   int64
	UpdatedAt time.Time
}
