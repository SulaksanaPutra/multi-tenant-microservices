package domain

import "time"

const (
	ExchangeCompanyEvents           = "company.events"
	RoutingKeyUserCreated           = "user.created"
	RoutingKeyWorkspaceReady        = "workspace.ready"
	QueueNotificationUserCreated    = "notification_service_user_created"
	QueueNotificationWorkspaceReady = "notification_service_workspace_ready"
)

type UserCreatedEvent struct {
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	TenantID  string    `json:"tenant_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkspaceReadyEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	OwnerEmail string `json:"owner_email"`
	TenantName string `json:"tenant_name"`
	TenantSlug string `json:"tenant_slug"`
	OwnerName  string `json:"owner_name"`
}
