package domain

// Permission describes an atomic capability exposed by a service that tenant
// admin roles can be composed from.
type Permission struct {
	Name        string
	Description string
}

// OrderServicePermissions is the single source of truth for the capabilities
// that order-service owns and declares to auth-service at startup.
var OrderServicePermissions = []Permission{
	{Name: "orders:write", Description: "Create new tenant order"},
	{Name: "orders:read", Description: "Read tenant orders"},
}