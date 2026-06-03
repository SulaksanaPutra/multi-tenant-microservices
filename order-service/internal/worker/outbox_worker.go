package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"order-service/internal/domain"
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

// RoutingStatusChecker allows the OutboxWorker to check the MIGRATING lock state
// of a tenant before publishing. Implemented by registry.RoutingRegistry.
type RoutingStatusChecker interface {
	GetStatus(tenantID string) string
}

type OutboxWorker struct {
	outboxRepository    OutboxRepository
	orderEventPublisher OrderEventPublisher
	routingStatus       RoutingStatusChecker
	wakeUpChan          chan struct{}
	debounceDelay       time.Duration
	pollInterval        time.Duration
	batchSize           int
}

func NewOutboxWorker(
	outboxRepository OutboxRepository,
	publisher OrderEventPublisher,
	routingStatus RoutingStatusChecker,
) *OutboxWorker {
	return &OutboxWorker{
		outboxRepository:    outboxRepository,
		orderEventPublisher: publisher,
		routingStatus:       routingStatus,
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
		if err := w.outboxRepository.RecoverStuckClaims(ctx, eventType); err != nil {
			log.Printf("OutboxWorker Warning: Stuck-claim recovery failed for '%s': %v", eventType, err)
		}
		w.processBatch(ctx, eventType)
	}
}

func (w *OutboxWorker) processBatch(ctx context.Context, eventType string) {
	messages, err := w.outboxRepository.FetchAndClaimBatch(ctx, eventType, w.batchSize)
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
				_ = w.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}

			// MIGRATING guard: if the tenant is currently being migrated to a dedicated
			// container, skip publishing and leave the message in PENDING state.
			// The message will be re-claimed on the next poll cycle after the lock clears.
			if w.routingStatus != nil && w.routingStatus.GetStatus(evt.TenantID) == "MIGRATING" {
				log.Printf("OutboxWorker: Skipping event id='%s' for tenant='%s' — tenant is MIGRATING.", msg.ID, evt.TenantID)
				// Reset status to PENDING so it is retried after migration completes.
				// We do this by calling MarkFailed with a descriptive sentinel error,
				// but since we don't want to exhaust retries, we re-set to PENDING directly
				// by not calling anything — FetchAndClaimBatch marked it PROCESSING.
				// We must release the claim; MarkFailed will reset it to PENDING (retry_count < max).
				_ = w.outboxRepository.MarkFailed(ctx, msg.ID, migratingErr(evt.TenantID))
				continue
			}

			pubErr = w.orderEventPublisher.PublishOrderCreated(ctx, evt)

		default:
			log.Printf("OutboxWorker Warning: Unknown event_type='%s' for id='%s'. Skipping.", eventType, msg.ID)
			continue
		}

		if pubErr != nil {
			log.Printf("OutboxWorker Warning: Publish failed for id='%s': %v", msg.ID, pubErr)
			_ = w.outboxRepository.MarkFailed(ctx, msg.ID, pubErr)
		} else {
			if markErr := w.outboxRepository.MarkPublished(ctx, msg.ID); markErr != nil {
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
