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
			outboxRepository := w.repoFactory(cfg)

			if err := outboxRepository.RecoverStuckClaims(ctx, eventType); err != nil {
				log.Printf("OutboxWorker Warning: Stuck-claim recovery failed for '%s': %v", eventType, err)
			}
			w.processTenantBatch(ctx, outboxRepository, eventType)
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

func (w *OutboxWorker) processTenantBatch(ctx context.Context, outboxRepository OutboxRepository, eventType string) {
	messages, err := outboxRepository.FetchAndClaimBatch(ctx, eventType, w.batchSize)
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
				_ = outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}

			// MIGRATING guard: if the tenant is currently being migrated to a dedicated
			// container, skip publishing and leave the message in PENDING state.
			// The message will be re-claimed on the next poll cycle after the lock clears.
			if w.routingStatus != nil && w.routingStatus.GetStatus(evt.TenantID) == "MIGRATING" {
				log.Printf("OutboxWorker: Skipping event id='%s' for tenant='%s' — tenant is MIGRATING.", msg.ID, evt.TenantID)
				_ = outboxRepository.MarkFailed(ctx, msg.ID, migratingErr(evt.TenantID))
				continue
			}

			pubErr = w.orderEventPublisher.PublishOrderCreated(ctx, evt)

		default:
			log.Printf("OutboxWorker Warning: Unknown event_type='%s' for id='%s'. Skipping.", eventType, msg.ID)
			continue
		}

		if pubErr != nil {
			log.Printf("OutboxWorker Warning: Publish failed for id='%s': %v", msg.ID, pubErr)
			_ = outboxRepository.MarkFailed(ctx, msg.ID, pubErr)
		} else {
			if markErr := outboxRepository.MarkPublished(ctx, msg.ID); markErr != nil {
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

// migratingErr is a sentinel error type used when a tenant is MIGRATING.
// It is passed to MarkFailed to reset the claim without logging a loud error.
type migratingError struct{ tenantID string }

func (e migratingError) Error() string {
	return "tenant " + e.tenantID + " is MIGRATING — deferred for retry"
}

func migratingErr(tenantID string) error { return migratingError{tenantID: tenantID} }
