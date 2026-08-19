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

func (outboxWorker *OutboxWorker) Poke() {
	select {
	case outboxWorker.wakeUpChan <- struct{}{}:
	default:
	}
}

func (outboxWorker *OutboxWorker) Start(ctx context.Context) {
	log.Printf("OutboxWorker background loop started (debounce=%v, poll=%v, batch=%d).",
		outboxWorker.debounceDelay, outboxWorker.pollInterval, outboxWorker.batchSize)

	outboxWorker.recoverAndProcess(ctx)

	ticker := time.NewTicker(outboxWorker.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("OutboxWorker shutting down cleanly.")
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

	outboxWorker.recoverAndProcess(ctx)
}

func (outboxWorker *OutboxWorker) recoverAndProcess(ctx context.Context) {
	eventTypes := []string{
		domain.RoutingKeyWorkspaceInitiated,
		domain.RoutingKeyWorkspaceReady,
		domain.RoutingKeyInfrastructureLocking,
		domain.RoutingKeyInfraChanged,
		domain.RoutingKeyTenantMigrationFailed,
	}
	for _, eventType := range eventTypes {
		if err := outboxWorker.outboxRepository.RecoverStuckClaims(ctx, eventType); err != nil {
			log.Printf("OutboxWorker Warning: Stuck-claim recovery failed for '%s': %v", eventType, err)
		}
		outboxWorker.processBatch(ctx, eventType)
	}
}

func (outboxWorker *OutboxWorker) processBatch(ctx context.Context, eventType string) {
	messages, err := outboxWorker.outboxRepository.ListAndClaimBatch(ctx, eventType, outboxWorker.batchSize)
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
				_ = outboxWorker.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = outboxWorker.publisher.PublishWorkspaceInitiated(ctx, evt)

		case domain.RoutingKeyWorkspaceReady:
			var evt domain.WorkspaceReadyEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = outboxWorker.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = outboxWorker.publisher.PublishWorkspaceReady(ctx, evt)

		case domain.RoutingKeyInfrastructureLocking:
			var evt domain.InfrastructureLockingEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = outboxWorker.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = outboxWorker.publisher.PublishInfrastructureLocking(ctx, evt)

		case domain.RoutingKeyInfraChanged:
			var evt domain.InfraChangedEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = outboxWorker.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = outboxWorker.publisher.PublishInfraChanged(ctx, evt)

		case domain.RoutingKeyTenantMigrationFailed:
			var evt domain.TenantMigrationFailedEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = outboxWorker.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = outboxWorker.publisher.PublishMigrationFailed(ctx, evt)

		default:
			log.Printf("OutboxWorker Warning: Unknown event_type='%s' for id='%s'. Skipping.", eventType, msg.ID)
			continue
		}

		if pubErr != nil {
			log.Printf("OutboxWorker Warning: Publish failed for id='%s': %v", msg.ID, pubErr)
			_ = outboxWorker.outboxRepository.MarkFailed(ctx, msg.ID, pubErr)
		} else {
			if markErr := outboxWorker.outboxRepository.MarkPublished(ctx, msg.ID); markErr != nil {
				log.Printf("OutboxWorker Error: MarkPublished failed for id='%s': %v", msg.ID, markErr)
			}
		}
	}

	if len(messages) == outboxWorker.batchSize {
		log.Printf("OutboxWorker: Full batch for '%s' — re-poking for more.", eventType)
		outboxWorker.Poke()
	}
}
