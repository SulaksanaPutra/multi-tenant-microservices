package consumer

import (
	"context"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
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
	DeclareAndBindExclusiveQueue(exchangeName, routingKey string) (string, error)
	Consume(queueName, consumerTag string) (<-chan rabbitmq.Delivery, error)
}

// =============================================================================
// Domain Service Contracts
// =============================================================================

// OrderDBReadyPublisher is the consumer-side interface for publishing tenant DB-ready events.
type OrderDBReadyPublisher interface {
	PublishTenantOrderDBReady(ctx context.Context, evt domain.TenantOrderDBReadyEvent) error
}

// MigrationService is the consumer-side interface for running tenant DB migrations.
type MigrationService interface {
	MigrateTenantDB(ctx context.Context, dsn, schemaName string) error
}
