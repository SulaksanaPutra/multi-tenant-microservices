package consumer

import (
	"context"

	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/service"
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

// UserService is the consumer-side interface for user creation from workspace events.
type UserService interface {
	CreateUserFromWorkspace(ctx context.Context, input service.CreateUserFromWorkspaceInput) error
}
