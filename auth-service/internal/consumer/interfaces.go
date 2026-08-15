package consumer

import (
	"context"

	"auth-service/internal/infrastructure/rabbitmq"
	"auth-service/internal/service"
)

// =============================================================================
// Transport & Infrastructure Contracts
// =============================================================================

// AMQPClient is the consumer-side interface defining the AMQP transport contract.
type AMQPClient interface {
	ConnContext() context.Context
	WaitUntilReady(ctx context.Context) error
	DeclareExchange(name, kind string) error
	DeclareAndBindQueue(queueName, exchangeName, routingKey string, args map[string]interface{}) error
	Consume(queueName, consumerTag string) (<-chan rabbitmq.Delivery, error)
}

// TxManager is the consumer-side interface for transactional coordination.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

// InboxService is the consumer-side interface for idempotent event claiming.
type InboxService interface {
	ClaimEvent(txCtx context.Context, input service.ClaimInboxInput) (bool, error)
}

// =============================================================================
// Domain Service Contracts
// =============================================================================

// MembershipService is the consumer-side interface for user membership management.
type MembershipService interface {
	AddMembership(ctx context.Context, userID, tenantID string) error
}
