package worker

import (
	"context"

	"tenant-service/internal/domain"
)

// =============================================================================
// Persistence Contracts
// =============================================================================

// OutboxRepository is the worker-side interface for outbox claim and persistence operations.
type OutboxRepository interface {
	RecoverStuckClaims(ctx context.Context, eventType string) error
	FetchAndClaimBatch(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error)
	MarkFailed(ctx context.Context, id string, err error) error
	MarkPublished(ctx context.Context, id string) error
}

// =============================================================================
// Publisher Contracts
// =============================================================================

// TenantEventPublisher is the worker-side interface for broadcasting domain events to AMQP.
type TenantEventPublisher interface {
	PublishWorkspaceInitiated(ctx context.Context, evt domain.WorkspaceInitiatedEvent) error
	PublishWorkspaceReady(ctx context.Context, evt domain.WorkspaceReadyEvent) error
	PublishInfrastructureLocking(ctx context.Context, evt domain.InfrastructureLockingEvent) error
	PublishInfraChanged(ctx context.Context, evt domain.InfraChangedEvent) error
	PublishMigrationFailed(ctx context.Context, evt domain.TenantMigrationFailedEvent) error
}
