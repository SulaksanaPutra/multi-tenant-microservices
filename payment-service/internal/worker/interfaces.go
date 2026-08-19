package worker

import (
	"context"
	"time"

	"payment-service/internal/domain"
)

// =============================================================================
// Persistence Contracts
// =============================================================================

// OutboxRepository is the worker-side interface for outbox claim and persistence operations.
type OutboxRepository interface {
	FetchPending(ctx context.Context, limit int) ([]*domain.OutboxMessage, error)
	RecoverStuckClaims(ctx context.Context) error
	MarkFailed(ctx context.Context, eventID string, reason string) error
	MarkPublished(ctx context.Context, eventID string) error
}

// =============================================================================
// Publisher Contracts
// =============================================================================

// EventPublisher is the worker-side interface for broadcasting generic AMQP payloads.
type EventPublisher interface {
	PublishEvent(ctx context.Context, routingKey string, payload []byte) error
}

// =============================================================================
// Domain Service Contracts
// =============================================================================

// PaymentSweeper is the worker-side interface for payment expiration operations.
type PaymentSweeper interface {
	SweepExpiredPayments(ctx context.Context, ttl time.Duration) (int, error)
}
