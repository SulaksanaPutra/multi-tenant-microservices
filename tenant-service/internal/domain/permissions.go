package domain

// Permission describes an atomic capability exposed by a service that tenant
// admin roles can be composed from.
type Permission struct {
	Name        string
	Description string
}

// TenantServicePermissions is the single source of truth for the capabilities
// that tenant-service owns and declares to auth-service at startup.
var TenantServicePermissions = []Permission{
	{Name: "tenants:read", Description: "Read tenant workspace details"},
	{Name: "tenants:write", Description: "Update tenant workspace details and plan"},
}