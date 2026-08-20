package domain

import "time"

const (
	ExchangeCompanyEvents           = "company.events"
	RoutingKeyUserCreated           = "user.created"
	RoutingKeyWorkspaceReady        = "workspace.ready"
	RoutingKeyOrderCreated          = "order.created"
	QueueNotificationUserCreated    = "notification_service_user_created"
	QueueNotificationWorkspaceReady = "notification_service_workspace_ready"
	QueueNotificationOrderCreated   = "notification_service_order_created"
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

type OrderCreatedEvent struct {
	EventID    string  `json:"event_id"`
	TenantID   string  `json:"tenant_id"`
	OrderID    string  `json:"order_id"`
	CustomerID string  `json:"customer_id"`
	Amount     float64 `json:"amount"`
	Currency   string  `json:"currency"`
	Status     string  `json:"status"`
}

