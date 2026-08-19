package worker

import (
	"context"

	"user-service/internal/domain"
)

// =============================================================================
// Persistence Contracts
// =============================================================================

// OutboxRepository is the worker-side interface for outbox claim and persistence operations.
type OutboxRepository interface {
	RecoverStuckClaims(ctx context.Context, eventType string) error
	ListAndClaimBatch(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error)
	MarkFailed(ctx context.Context, id string, err error) error
	MarkPublished(ctx context.Context, id string) error
}

// =============================================================================
// Publisher Contracts
// =============================================================================

// UserEventPublisher is the worker-side interface for broadcasting user events to AMQP.
type UserEventPublisher interface {
	PublishUserCreated(ctx context.Context, evt domain.UserCreatedEvent) error
}
