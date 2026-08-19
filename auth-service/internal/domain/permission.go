package domain

import "time"

type Permission struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Service     string    `json:"service"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Role struct {
	ID          string       `json:"id"`
	TenantID    *string      `json:"tenant_id,omitempty"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	IsSystem    bool         `json:"is_system"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Permissions []Permission `json:"permissions,omitempty"`
}

type UserRole struct {
	UserID     string    `json:"user_id"`
	TenantID   string    `json:"tenant_id"`
	RoleID     string    `json:"role_id"`
	AssignedAt time.Time `json:"assigned_at"`
	AssignedBy *string   `json:"assigned_by,omitempty"`
	Role       *Role     `json:"role,omitempty"`
}

type UserRoleAssignment struct {
	UserID   string
	RoleID   string
	RoleName string
}

type UserPermissionVersion struct {
	UserID    string    `json:"user_id"`
	TenantID  string    `json:"tenant_id"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}
