package domain

// Permission describes an atomic capability exposed by a service that tenant
// admin roles can be composed from.
type Permission struct {
	Name        string
	Description string
}

// NotificationServicePermissions is the single source of truth for the
// capabilities that notification-service owns and declares to auth-service at
// startup.
var NotificationServicePermissions = []Permission{
	{Name: "notifications:read", Description: "Read user notifications"},
}