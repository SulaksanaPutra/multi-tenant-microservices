package domain

import "time"

// Event-fed membership copy contract: auth-service consumes user.created to
// keep its user_tenant_memberships copy eventually consistent.
const (
	ExchangeCompanyEvents        = "company.events"
	ExchangeCompanyEventsDLX     = "company.events.dlx"
	RoutingKeyUserCreated        = "user.created"
	QueueAuthUserCreated         = "auth_service_user_created_membership"
	QueueAuthUserCreatedDLQ      = "auth_service_user_created_membership_dlq"
	MaxAuthUserCreatedDeliveries = 3
)

// UserCreatedEvent mirrors the payload published by user-service.
type UserCreatedEvent struct {
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	TenantID  string    `json:"tenant_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
