package domain

type Permission struct {
	Name        string
	Description string
}

var TenantServicePermissions = []Permission{
	{Name: "tenants:read", Description: "Read tenant workspace details"},
	{Name: "tenants:write", Description: "Update tenant workspace details and plan"},
}
