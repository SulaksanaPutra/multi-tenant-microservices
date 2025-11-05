package domain

const (
	ExchangeCompanyEvents               = "company.events"
	RoutingKeyWorkspaceInitiated        = "workspace.initiated"
	RoutingKeyInfrastructureProvisioned = "infrastructure.provisioned"
	QueueInfraProvisionerWorkspace      = "infra_provisioner_workspace_initiated"
)

type WorkspaceInitiatedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Plan       string `json:"plan"`
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
}

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
