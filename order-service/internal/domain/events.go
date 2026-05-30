package domain

const (
	ExchangeCompanyEvents               = "company.events"
	RoutingKeyInfrastructureProvisioned = "infrastructure.provisioned"
	RoutingKeyTenantOrderDBReady        = "tenant.order_db.ready"
	RoutingKeyInfraChanged              = "tenant.infrastructure_changed"
	RoutingKeyOrderCreated              = "order.created"
	RoutingKeyInfrastructureLocking     = "tenant.infrastructure_locking"
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

// OrderCreatedEvent is published by the order-service outbox worker when an order is created.
type OrderCreatedEvent struct {
	EventID    string  `json:"event_id"`
	TenantID   string  `json:"tenant_id"`
	OrderID    string  `json:"order_id"`
	CustomerID string  `json:"customer_id"`
	Amount     float64 `json:"amount"`
	Status     string  `json:"status"`
}

// InfrastructureLockingEvent is broadcast (fanout) by tenant-service to pause
// all order-service replicas for a specific tenant during data migration.
type InfrastructureLockingEvent struct {
	EventID  string `json:"event_id"`
	TenantID string `json:"tenant_id"`
}
