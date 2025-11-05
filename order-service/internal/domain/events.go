package domain

const (
	ExchangeCompanyEvents               = "company.events"
	RoutingKeyInfrastructureProvisioned = "infrastructure.provisioned"
	RoutingKeyTenantOrderDBReady        = "tenant.order_db.ready"
	RoutingKeyInfraChanged            = "tenant.infrastructure_changed"
	QueueOrderServiceInfraProvisioned   = "order_service_infrastructure_provisioned"
)

type InfrastructureProvisionedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Plan       string `json:"plan"`
	DBHost     string `json:"db_host"`
	DBPort     int    `json:"db_port"`
	DBName     string `json:"db_name"`
	DBUser     string `json:"db_user"`
	SchemaName string `json:"schema_name"`
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
