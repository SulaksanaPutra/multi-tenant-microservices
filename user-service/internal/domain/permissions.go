package domain

type Permission struct {
	Name        string
	Description string
}

var UserServicePermissions = []Permission{
	{Name: "users:read", Description: "Read user profiles"},
	{Name: "users:write", Description: "Update user profiles"},
}
