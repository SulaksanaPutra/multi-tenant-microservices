package domain

type Permission struct {
	Name        string
	Description string
}

var OrderServicePermissions = []Permission{
	{Name: "orders:write", Description: "Create new tenant order"},
	{Name: "orders:read", Description: "Read tenant orders"},
}
