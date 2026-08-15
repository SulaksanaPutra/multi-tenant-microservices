package consumer

import (
	"context"

	"infra-provisioner/internal/domain"
	"infra-provisioner/internal/infrastructure/rabbitmq"
)

// =============================================================================
// Transport & Infrastructure Contracts
// =============================================================================

// AMQPClient is the consumer-side interface defining the AMQP transport contract.
type AMQPClient interface {
	ConnContext() context.Context
	WaitUntilReady(ctx context.Context) error
	DeclareExchange(name, kind string) error
	DeclareAndBindQueue(queueName, exchangeName, routingKey string) error
	Consume(queueName, consumerTag string) (<-chan rabbitmq.Delivery, error)
}

// =============================================================================
// Domain Service Contracts
// =============================================================================

// InfrastructureEventPublisher is the consumer-side interface for publishing infrastructure lifecycle events.
type InfrastructureEventPublisher interface {
	PublishInfrastructureProvisioned(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error
	PublishTenantMigrationFailed(ctx context.Context, evt domain.TenantMigrationFailedEvent) error
}

// Provisioner is the consumer-side interface for provisioning dedicated tenant containers.
type Provisioner interface {
	ProvisionDedicatedContainer(ctx context.Context, tenantID, infraMasterSecret string, domainSecrets map[string]string) (host string, port int, dbName, dbUser string, err error)
}

// Migrator is the consumer-side interface for tenant data migration and schema lifecycle operations.
type Migrator interface {
	CheckSchemaExists(ctx context.Context, dsn, schemaName string) (bool, error)
	LockSchema(ctx context.Context, sharedDSN, schemaName, lockedSchemaName string) error
	RestoreSchema(ctx context.Context, sharedDSN, lockedSchemaName, originalSchemaName string) error
	MigrateData(ctx context.Context, sourceHost string, sourcePort int, sourceUser, sourcePass, sourceDB, sourceSchema string, targetHost string, targetPort int, targetUser, targetPass, targetDB, targetSchema string) error
	TableHasRows(ctx context.Context, dsn, schema, table string) (bool, error)
	DropSchemaIfExists(ctx context.Context, dsn, schemaName string) error
	ProvisionTenantDatabase(ctx context.Context, host string, port int, superUser, superPass, dbName, roleName, rolePass string) error
	DropTenantDatabase(ctx context.Context, host string, port int, superUser, superPass, dbName, roleName string) error
	DestroyContainer(ctx context.Context, containerName string) error
}
