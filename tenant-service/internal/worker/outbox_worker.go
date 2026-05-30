package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"tenant-service/internal/domain"
)

const (
	defaultDebounceDelay = 10 * time.Millisecond
	defaultPollInterval  = 1 * time.Second
	defaultBatchSize     = 50
)

// OutboxRepository is the consumer-side interface expected by OutboxWorker.
type OutboxRepository interface {
	RecoverStuckClaims(ctx context.Context, eventType string) error
	FetchAndClaimBatch(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error)
	MarkFailed(ctx context.Context, id string, err error) error
	MarkPublished(ctx context.Context, id string) error
}

// TenantEventPublisher is the consumer-side interface expected by OutboxWorker.
type TenantEventPublisher interface {
	PublishWorkspaceInitiated(ctx context.Context, evt domain.WorkspaceInitiatedEvent) error
	PublishWorkspaceReady(ctx context.Context, evt domain.WorkspaceReadyEvent) error
	PublishInfrastructureLocking(ctx context.Context, evt domain.InfrastructureLockingEvent) error
	PublishInfraChanged(ctx context.Context, evt domain.InfraChangedEvent) error
	PublishMigrationFailed(ctx context.Context, evt domain.TenantMigrationFailedEvent) error
}

type OutboxWorker struct {
	outboxRepository OutboxRepository
	publisher        TenantEventPublisher
	wakeUpChan       chan struct{}
	debounceDelay    time.Duration
	pollInterval     time.Duration
	batchSize        int
}

func NewOutboxWorker(
	outboxRepository OutboxRepository,
	publisher TenantEventPublisher,
) *OutboxWorker {
	return &OutboxWorker{
		outboxRepository: outboxRepository,
		publisher:        publisher,
		wakeUpChan:       make(chan struct{}, 1),
		debounceDelay:    defaultDebounceDelay,
		pollInterval:     defaultPollInterval,
		batchSize:        defaultBatchSize,
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
	log.Printf("OutboxWorker background loop started (debounce=%v, poll=%v, batch=%d).",
		w.debounceDelay, w.pollInterval, w.batchSize)

	w.recoverAndProcess(ctx)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("OutboxWorker shutting down cleanly.")
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

	w.recoverAndProcess(ctx)
}

func (w *OutboxWorker) recoverAndProcess(ctx context.Context) {
	eventTypes := []string{
		domain.RoutingKeyWorkspaceInitiated,
		domain.RoutingKeyWorkspaceReady,
		domain.RoutingKeyInfrastructureLocking,
		domain.RoutingKeyInfraChanged,
		domain.RoutingKeyTenantMigrationFailed,
	}
	for _, eventType := range eventTypes {
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
		case domain.RoutingKeyWorkspaceInitiated:
			var evt domain.WorkspaceInitiatedEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = w.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = w.publisher.PublishWorkspaceInitiated(ctx, evt)

		case domain.RoutingKeyWorkspaceReady:
			var evt domain.WorkspaceReadyEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = w.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = w.publisher.PublishWorkspaceReady(ctx, evt)

		case domain.RoutingKeyInfrastructureLocking:
			var evt domain.InfrastructureLockingEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = w.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = w.publisher.PublishInfrastructureLocking(ctx, evt)

		case domain.RoutingKeyInfraChanged:
			var evt domain.InfraChangedEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = w.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = w.publisher.PublishInfraChanged(ctx, evt)

		case domain.RoutingKeyTenantMigrationFailed:
			var evt domain.TenantMigrationFailedEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = w.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = w.publisher.PublishMigrationFailed(ctx, evt)

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
