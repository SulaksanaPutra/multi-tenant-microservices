package worker

import (
	"context"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/tenantdb"
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

// OutboxRepoFactory builds an outbox repository bound to a tenant's resolved database/schema.
type OutboxRepoFactory func(cfg tenantdb.Config) OutboxRepository

// =============================================================================
// Tenant Resolution Contracts
// =============================================================================

// TenantDBResolver resolves the database configuration for a tenant.
// It returns domain.ErrTenantMigrating when the tenant is locked for migration.
type TenantDBResolver interface {
	GetTenantDB(ctx context.Context, tenantID string) (tenantdb.Config, error)
}

// TenantLister enumerates the tenants currently materialized in the local RoutingRegistry.
type TenantLister interface {
	TenantIDs() []string
}

// RoutingStatusChecker allows the OutboxWorker to check the MIGRATING lock state
// of a tenant before polling. Implemented by registry.RoutingRegistry.
type RoutingStatusChecker interface {
	GetStatus(tenantID string) string
}

// =============================================================================
// Publisher Contracts
// =============================================================================

// OrderEventPublisher is the worker-side interface for broadcasting order events to AMQP.
type OrderEventPublisher interface {
	PublishOrderCreated(ctx context.Context, evt domain.OrderCreatedEvent) error
}
