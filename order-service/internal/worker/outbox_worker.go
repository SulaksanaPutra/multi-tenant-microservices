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

func (outboxWorker *OutboxWorker) Poke() {
	select {
	case outboxWorker.wakeUpChan <- struct{}{}:
	default:
	}
}

func (outboxWorker *OutboxWorker) Start(ctx context.Context) {
	log.Printf("OutboxWorker: Started (debounce=%v, batch=%d, poll=%v)", outboxWorker.debounceDelay, outboxWorker.batchSize, outboxWorker.pollInterval)

	ticker := time.NewTicker(outboxWorker.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("OutboxWorker: Shutting down.")
			return
		case <-outboxWorker.wakeUpChan:
			outboxWorker.debounceAndProcess(ctx)
		case <-ticker.C:
			outboxWorker.recoverAndProcess(ctx)
		}
	}
}

func (outboxWorker *OutboxWorker) debounceAndProcess(ctx context.Context) {
	timer := time.NewTimer(outboxWorker.debounceDelay)
	defer timer.Stop()

drainLoop:
	for {
		select {
		case <-ctx.Done():
			return
		case <-outboxWorker.wakeUpChan:
		case <-timer.C:
			break drainLoop
		}
	}

	outboxWorker.processBatch(ctx, domain.RoutingKeyOrderCreated)
}

func (outboxWorker *OutboxWorker) recoverAndProcess(ctx context.Context) {
	for _, eventType := range []string{domain.RoutingKeyOrderCreated} {
		outboxWorker.forEachActiveTenant(ctx, func(cfg tenantdb.Config) {
			outboxRepository := outboxWorker.repoFactory(cfg)

			if err := outboxRepository.RecoverStuckClaims(ctx, eventType); err != nil {
				log.Printf("OutboxWorker Warning: Stuck-claim recovery failed for '%s': %v", eventType, err)
			}
			outboxWorker.processTenantBatch(ctx, outboxRepository, eventType)
		})
	}
}

func (outboxWorker *OutboxWorker) processBatch(ctx context.Context, eventType string) {
	outboxWorker.forEachActiveTenant(ctx, func(cfg tenantdb.Config) {
		outboxWorker.processTenantBatch(ctx, outboxWorker.repoFactory(cfg), eventType)
	})
}

func (outboxWorker *OutboxWorker) forEachActiveTenant(ctx context.Context, fn func(cfg tenantdb.Config)) {
	if outboxWorker.resolver == nil || outboxWorker.tenantLister == nil || outboxWorker.repoFactory == nil {
		log.Printf("OutboxWorker Warning: resolver, tenant lister or repo factory is nil; polling disabled")
		return
	}

	for _, tenantID := range outboxWorker.tenantLister.TenantIDs() {
		if outboxWorker.routingStatus != nil && outboxWorker.routingStatus.GetStatus(tenantID) == "MIGRATING" {
			log.Printf("OutboxWorker: Skipping tenant='%s' — tenant is MIGRATING (migration lock active).", tenantID)
			continue
		}

		cfg, err := outboxWorker.resolver.GetTenantDB(ctx, tenantID)
		if err != nil {
			log.Printf("OutboxWorker Warning: Failed to resolve DB config for tenant='%s': %v", tenantID, err)
			continue
		}

		fn(cfg)
	}
}

func (outboxWorker *OutboxWorker) processTenantBatch(ctx context.Context, outboxRepository OutboxRepository, eventType string) {
	messages, err := outboxRepository.ListAndClaimBatch(ctx, eventType, outboxWorker.batchSize)
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

			if outboxWorker.routingStatus != nil && outboxWorker.routingStatus.GetStatus(evt.TenantID) == "MIGRATING" {
				log.Printf("OutboxWorker: Skipping event id='%s' for tenant='%s' — tenant is MIGRATING.", msg.ID, evt.TenantID)
				_ = outboxRepository.MarkFailed(ctx, msg.ID, migratingErr(evt.TenantID))
				continue
			}

			pubErr = outboxWorker.orderEventPublisher.PublishOrderCreated(ctx, evt)

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

	if len(messages) == outboxWorker.batchSize {
		log.Printf("OutboxWorker: Full batch for '%s' — re-poking for more.", eventType)
		outboxWorker.Poke()
	}
}

type migratingError struct{ tenantID string }

func (e migratingError) Error() string {
	return "tenant " + e.tenantID + " is MIGRATING — deferred for retry"
}

func migratingErr(tenantID string) error { return migratingError{tenantID: tenantID} }
