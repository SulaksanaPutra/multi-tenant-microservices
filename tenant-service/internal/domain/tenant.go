package domain

import "time"

type Tenant struct {
	ID         string
	Name       string
	Slug       string
	OwnerEmail string
	OwnerName  string
	Plan       string
	Status     string
	CreatedAt  time.Time
}

type TenantInfra struct {
	TenantID    string
	ServiceName string
	DBHost      string
	DBPort      int
	DBName      string
	DBUser      string
	SchemaName  string
}
