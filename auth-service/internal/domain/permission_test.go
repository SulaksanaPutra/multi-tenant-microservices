package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPermission_JSON(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	perm := Permission{
		ID:          "perm_read",
		Name:        "auth:read",
		Service:     "auth-service",
		Description: "Read auth data",
		CreatedAt:   now,
	}

	data, err := json.Marshal(perm)
	if err != nil {
		t.Fatalf("failed to marshal Permission: %v", err)
	}

	var unmarshaled Permission
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal Permission: %v", err)
	}

	if unmarshaled.ID != perm.ID || unmarshaled.Name != perm.Name || unmarshaled.Service != perm.Service {
		t.Errorf("unmarshaled Permission mismatch: got %+v, want %+v", unmarshaled, perm)
	}
}

func TestRole_JSON(t *testing.T) {
	tenantID := "tnt_123"
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	role := Role{
		ID:          "role_admin",
		TenantID:    &tenantID,
		Name:        "TenantAdmin",
		Description: "Tenant Administrator",
		IsSystem:    false,
		CreatedAt:   now,
		Permissions: []Permission{
			{ID: "p1", Name: "auth:read", Service: "auth-service"},
		},
	}

	data, err := json.Marshal(role)
	if err != nil {
		t.Fatalf("failed to marshal Role: %v", err)
	}

	var unmarshaled Role
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal Role: %v", err)
	}

	if unmarshaled.ID != role.ID || *unmarshaled.TenantID != tenantID || len(unmarshaled.Permissions) != 1 {
		t.Errorf("unmarshaled Role mismatch: got %+v, want %+v", unmarshaled, role)
	}
}

func TestUserRole_JSON(t *testing.T) {
	assignedBy := "usr_admin"
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	ur := UserRole{
		UserID:     "usr_100",
		TenantID:   "tnt_123",
		RoleID:     "role_admin",
		AssignedAt: now,
		AssignedBy: &assignedBy,
		Role: &Role{
			ID:   "role_admin",
			Name: "TenantAdmin",
		},
	}

	data, err := json.Marshal(ur)
	if err != nil {
		t.Fatalf("failed to marshal UserRole: %v", err)
	}

	var unmarshaled UserRole
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal UserRole: %v", err)
	}

	if unmarshaled.UserID != ur.UserID || unmarshaled.RoleID != ur.RoleID || *unmarshaled.AssignedBy != assignedBy {
		t.Errorf("unmarshaled UserRole mismatch: got %+v, want %+v", unmarshaled, ur)
	}
}

func TestUserPermissionVersion_Struct(t *testing.T) {
	now := time.Now()
	upv := UserPermissionVersion{
		UserID:    "usr_100",
		TenantID:  "tnt_123",
		Version:   5,
		UpdatedAt: now,
	}

	if upv.UserID != "usr_100" || upv.TenantID != "tnt_123" || upv.Version != 5 {
		t.Errorf("unexpected UserPermissionVersion state: %+v", upv)
	}
}
