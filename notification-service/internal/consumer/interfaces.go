package consumer

import (
	"context"

	"notification-service/internal/domain"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
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
	ListBarrierEvents(txCtx context.Context, tenantID string) ([]domain.InboxMessage, error)
}

// =============================================================================
// Domain Service Contracts
// =============================================================================

// NotificationService is the consumer-side interface for user-created and
// workspace-ready notification flows.
type NotificationService interface {
	ProcessEventAndTrySendWelcome(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error)
	UpdateNotificationStatus(ctx context.Context, logID string, status string) error
	HasSentNotification(ctx context.Context, tenantID string) (bool, error)
}

// OrderNotificationService is the consumer-side interface for order notification flows.
type OrderNotificationService interface {
	CreateOrderNotification(ctx context.Context, evt domain.OrderCreatedEvent) error
}

// AuthClient is the consumer-side interface for auth token retrieval.
type AuthClient interface {
	FetchSetupToken(ctx context.Context, userID, tenantID, email string) (string, error)
}

// Mailer is the consumer-side interface for sending outbound email.
type Mailer interface {
	SendWelcomeEmail(recipientEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string, error)
}
