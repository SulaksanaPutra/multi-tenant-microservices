package domain

const (
	ExchangeCompanyEvents             = "company.events"
	RoutingKeyWorkspaceInitiated      = "workspace.initiated"
	RoutingKeyWorkspaceReady          = "workspace.ready"
	RoutingKeyTenantOrderDBReady      = "tenant.order_db.ready"
	RoutingKeyInfraChanged            = "tenant.infrastructure_changed"
	RoutingKeyInfrastructureLocking   = "tenant.infrastructure_locking"
	RoutingKeyTenantMigrationFailed   = "tenant.migration_failed"
	QueueTenantServiceOrderReady      = "tenant_service_order_db_ready"
	QueueTenantServiceMigrationFailed = "tenant_service_migration_failed"
)

const (
	StatusActive    = "active"
	StatusPending   = "pending"
	StatusMigrating = "MIGRATING"
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
	TenantName string `json:"tenant_name"`
	TenantSlug string `json:"tenant_slug"`
	OwnerName  string `json:"owner_name"`
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

type InfrastructureLockingEvent struct {
	EventID  string `json:"event_id"`
	TenantID string `json:"tenant_id"`
}

type TenantMigrationFailedEvent struct {
	EventID  string `json:"event_id"`
	TenantID string `json:"tenant_id"`
	Reason   string `json:"reason,omitempty"`
}

