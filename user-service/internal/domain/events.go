package domain

import "time"

const (
	ExchangeCompanyEvents              = "company.events"
	RoutingKeyWorkspaceInitiated       = "workspace.initiated"
	RoutingKeyUserCreated              = "user.created"
	QueueUserServiceWorkspaceInitiated = "user_service_workspace_initiated"
)

type WorkspaceInitiatedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Plan       string `json:"plan"`
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
}

type UserCreatedEvent struct {
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	TenantID  string    `json:"tenant_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
