package consumer

import (
	"context"

	"payment-service/internal/infrastructure/rabbitmq"
	"payment-service/internal/service"
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
	ClaimEvent(txCtx context.Context, input service.ClaimInboxInput) (bool, error)
}

// =============================================================================
// Domain Service Contracts
// =============================================================================

// PaymentService is the consumer-side interface for payment DB lifecycle operations.
type PaymentService interface {
	InitiatePayment(ctx context.Context, input service.InitiatePaymentInput) (*service.PaymentOutput, error)
	CompleteInstructionGeneration(ctx context.Context, input service.CompleteInstructionInput) error
	FailInstructionGeneration(ctx context.Context, input service.FailInstructionInput) error
}

// PaymentProviderService is the consumer-side interface for external gateway fallback execution.
type PaymentProviderService interface {
	ExecuteFallback(ctx context.Context, input service.ExecuteFallbackInput) (*service.ExecuteFallbackOutput, error)
}
