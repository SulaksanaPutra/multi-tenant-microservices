package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/tenantdb"
)

const (
	defaultDebounceDelay = 10 * time.Millisecond
	defaultPollInterval  = 5 * time.Second
	defaultBatchSize     = 50
)

// OutboxRepository is the consumer-side interface expected by OutboxWorker.
type OutboxRepository interface {
	RecoverStuckClaims(ctx context.Context, eventType string) error
	FetchAndClaimBatch(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error)
	MarkFailed(ctx context.Context, id string, err error) error
	MarkPublished(ctx context.Context, id string) error
}

// OrderEventPublisher is the consumer-side interface expected by OutboxWorker.
type OrderEventPublisher interface {
	PublishOrderCreated(ctx context.Context, evt domain.OrderCreatedEvent) error
}

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

// OutboxRepoFactory builds an outbox repository bound to a tenant's resolved database/schema.
type OutboxRepoFactory func(cfg tenantdb.Config) OutboxRepository

// OutboxWorker polls the per-tenant outbox tables of every tenant materialized in the
// local RoutingRegistry and publishes order.created events to RabbitMQ.
//
// Outbox rows are physically distributed: shared-plan tenants store them in
// <tenant>_order_db.outbox inside the shared cluster, while dedicated-plan tenants
// store them in public.outbox of their private container. The worker therefore
// iterates the registry and resolves each tenant through tenantdb.Resolver.
//
// Migration safety: before executing the SELECT FOR UPDATE SKIP LOCKED query for a
// tenant, the worker consults the in-memory RoutingRegistry. If the tenant's status
// is MIGRATING it is skipped entirely, respecting the deterministic schema lock.
type OutboxWorker struct {
	resolver            TenantDBResolver
	tenantLister        TenantLister
	routingStatus       RoutingStatusChecker
	repoFactory         OutboxRepoFactory
	orderEventPublisher OrderEventPublisher
	wakeUpChan          chan struct{}
	debounceDelay       time.Duration
	pollInterval        time.Duration
	batchSize           int
}

func NewOutboxWorker(
	resolver TenantDBResolver,
	tenantLister TenantLister,
	routingStatus RoutingStatusChecker,
	repoFactory OutboxRepoFactory,
	publisher OrderEventPublisher,
) *OutboxWorker {
	return &OutboxWorker{
		resolver:            resolver,
		tenantLister:        tenantLister,
		routingStatus:       routingStatus,
		repoFactory:         repoFactory,
		orderEventPublisher: publisher,
		wakeUpChan:          make(chan struct{}, 1),
		debounceDelay:       defaultDebounceDelay,
		pollInterval:        defaultPollInterval,
		batchSize:           defaultBatchSize,
	}
}

// Poke sends a non-blocking wake-up signal to the worker loop.
func (w *OutboxWorker) Poke() {
	select {
	case w.wakeUpChan <- struct{}{}:
	default:
	}
}

func (w *OutboxWorker) Start(ctx context.Context) {
	log.Printf("OutboxWorker: Started (debounce=%v, batch=%d, poll=%v)", w.debounceDelay, w.batchSize, w.pollInterval)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("OutboxWorker: Shutting down.")
			return
		case <-w.wakeUpChan:
			w.debounceAndProcess(ctx)
		case <-ticker.C:
			w.recoverAndProcess(ctx)
		}
	}
}

func (w *OutboxWorker) debounceAndProcess(ctx context.Context) {
	timer := time.NewTimer(w.debounceDelay)
	defer timer.Stop()

drainLoop:
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wakeUpChan:
		case <-timer.C:
			break drainLoop
		}
	}

	w.processBatch(ctx, domain.RoutingKeyOrderCreated)
}

func (w *OutboxWorker) recoverAndProcess(ctx context.Context) {
	for _, eventType := range []string{domain.RoutingKeyOrderCreated} {
		w.forEachActiveTenant(ctx, func(cfg tenantdb.Config) {
			repo := w.repoFactory(cfg)

			if err := repo.RecoverStuckClaims(ctx, eventType); err != nil {
				log.Printf("OutboxWorker Warning: Stuck-claim recovery failed for '%s': %v", eventType, err)
			}
			w.processTenantBatch(ctx, repo, eventType)
		})
	}
}

func (w *OutboxWorker) processBatch(ctx context.Context, eventType string) {
	w.forEachActiveTenant(ctx, func(cfg tenantdb.Config) {
		w.processTenantBatch(ctx, w.repoFactory(cfg), eventType)
	})
}

// forEachActiveTenant iterates every tenant in the RoutingRegistry, skipping any
// tenant whose status is MIGRATING before a single outbox query is executed.
func (w *OutboxWorker) forEachActiveTenant(ctx context.Context, fn func(cfg tenantdb.Config)) {
	if w.resolver == nil || w.tenantLister == nil || w.repoFactory == nil {
		log.Printf("OutboxWorker Warning: resolver, tenant lister or repo factory is nil; polling disabled")
		return
	}

	for _, tenantID := range w.tenantLister.TenantIDs() {
		if w.routingStatus != nil && w.routingStatus.GetStatus(tenantID) == "MIGRATING" {
			log.Printf("OutboxWorker: Skipping tenant='%s' — tenant is MIGRATING (migration lock active).", tenantID)
			continue
		}

		cfg, err := w.resolver.GetTenantDB(ctx, tenantID)
		if err != nil {
			log.Printf("OutboxWorker Warning: Failed to resolve DB config for tenant='%s': %v", tenantID, err)
			continue
		}

		fn(cfg)
	}
}

func (w *OutboxWorker) processTenantBatch(ctx context.Context, repo OutboxRepository, eventType string) {
	messages, err := repo.FetchAndClaimBatch(ctx, eventType, w.batchSize)
	if err != nil {
		log.Printf("OutboxWorker Error: Failed to claim outbox batch for '%s': %v", eventType, err)
		return
	}
	if len(messages) == 0 {
		return
	}

	log.Printf("OutboxWorker: Processing batch of %d '%s' messages.", len(messages), eventType)

	for _, msg := range messages {
		var pubErr error

		switch eventType {
		case domain.RoutingKeyOrderCreated:
			var evt domain.OrderCreatedEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = repo.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = w.orderEventPublisher.PublishOrderCreated(ctx, evt)

		default:
			log.Printf("OutboxWorker Warning: Unknown event_type='%s' for id='%s'. Skipping.", eventType, msg.ID)
			continue
		}

		if pubErr != nil {
			log.Printf("OutboxWorker Warning: Publish failed for id='%s': %v", msg.ID, pubErr)
			_ = repo.MarkFailed(ctx, msg.ID, pubErr)
		} else {
			if markErr := repo.MarkPublished(ctx, msg.ID); markErr != nil {
				log.Printf("OutboxWorker Error: MarkPublished failed for id='%s': %v", msg.ID, markErr)
			}
		}
	}

	// If we filled the entire batch, chain immediately to catch remaining rows.
	if len(messages) == w.batchSize {
		log.Printf("OutboxWorker: Full batch for '%s' — re-poking for more.", eventType)
		w.Poke()
	}
}
