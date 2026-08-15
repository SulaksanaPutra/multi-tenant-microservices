package consumer

import (
	"context"

	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/service"
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

// TxManager is the consumer-side interface for transactional coordination.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

// InboxService is the consumer-side interface for idempotent event claiming.
type InboxService interface {
	ClaimEvent(txCtx context.Context, eventID string) (bool, error)
}

// =============================================================================
// Domain Service Contracts
// =============================================================================

// TenantInfrastructureService is the consumer-side interface for processing infrastructure updates.
type TenantInfrastructureService interface {
	HandleInfrastructureUpdate(ctx context.Context, input service.InfrastructureUpdateInput) error
}

// MigrationRollbackService is the consumer-side interface for rolling back failed migrations.
// It exposes the business operation that resets a tenant to ACTIVE and stages the unfreeze
// broadcast; the repository work is owned by the Layer-2 service.
type MigrationRollbackService interface {
	RollbackFailedMigration(ctx context.Context, tenantID string) error
}
