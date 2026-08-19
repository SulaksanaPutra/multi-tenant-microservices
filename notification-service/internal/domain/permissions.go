package domain

type Permission struct {
	Name        string
	Description string
}

var NotificationServicePermissions = []Permission{
	{Name: "notifications:read", Description: "Read user notifications"},
}
