package domain

// Permission describes an atomic capability exposed by a service that tenant
// admin roles can be composed from.
type Permission struct {
	Name        string
	Description string
}

// UserServicePermissions is the single source of truth for the capabilities
// that user-service owns and declares to auth-service at startup.
var UserServicePermissions = []Permission{
	{Name: "users:read", Description: "Read user profiles"},
	{Name: "users:write", Description: "Update user profiles"},
}