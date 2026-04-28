package domain

const (
	ExchangeCompanyEvents        = "company.events"
	RoutingKeyWorkspaceInitiated = "workspace.initiated"
	RoutingKeyWorkspaceReady     = "workspace.ready"
	RoutingKeyTenantOrderDBReady = "tenant.order_db.ready"
	RoutingKeyInfraChanged       = "tenant.infrastructure_changed"
	QueueTenantServiceOrderReady = "tenant_service_order_db_ready"
)

type WorkspaceInitiatedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Plan       string `json:"plan"`
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
}

type WorkspaceReadyEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	OwnerEmail string `json:"owner_email"`
}

type TenantOrderDBReadyEvent struct {
	EventID     string `json:"event_id"`
	TenantID    string `json:"tenant_id"`
	ServiceName string `json:"service_name"`
	DBHost      string `json:"db_host"`
	DBPort      int    `json:"db_port"`
	DBName      string `json:"db_name"`
	DBUser      string `json:"db_user"`
	SchemaName  string `json:"schema_name"`
}

type InfraChangedEvent struct {
	EventID  string `json:"event_id"`
	TenantID string `json:"tenant_id"`
}
