package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"user-service/internal/publisher"
	"user-service/internal/repository"
)

const (
	defaultPollInterval = 5 * time.Second
	defaultBatchSize    = 50
)

type OutboxWorker struct {
	outboxRepo   repository.OutboxRepository
	publisher    publisher.UserEventPublisher
	eventType    string
	pollInterval time.Duration
	batchSize    int
}

func NewOutboxWorker(
	outboxRepo repository.OutboxRepository,
	pub publisher.UserEventPublisher,
	eventType string,
) *OutboxWorker {
	return &OutboxWorker{
		outboxRepo:   outboxRepo,
		publisher:    pub,
		eventType:    eventType,
		pollInterval: defaultPollInterval,
		batchSize:    defaultBatchSize,
	}
}

func (w *OutboxWorker) Start(ctx context.Context) {
	log.Printf("OutboxWorker [%s]: Started (poll=%v, batch=%d)", w.eventType, w.pollInterval, w.batchSize)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("OutboxWorker [%s]: Shutting down.", w.eventType)
			return
		case <-ticker.C:
			// Fallback sweep: also recovers stuck PROCESSING rows.
			if err := w.outboxRepo.RecoverStuckClaims(ctx, w.eventType); err != nil {
				log.Printf("OutboxWorker [%s] Warning: Stuck-claim recovery failed: %v", w.eventType, err)
			}
			w.processBatch(ctx)
		}
	}
}

// processBatch fetches PENDING rows and publishes them one by one.
// BUG (Challenge 2): FetchPendingBatch uses a plain SELECT with no row locking.
// Two concurrent worker instances will see the same rows and publish duplicates —
// the "Phantom Batch" race condition. This will be fixed in Challenge 2 with
// an atomic CTE + FOR UPDATE SKIP LOCKED query.
func (w *OutboxWorker) processBatch(ctx context.Context) {
	messages, err := w.outboxRepo.FetchPendingBatch(ctx, w.eventType, w.batchSize)
	if err != nil {
		log.Printf("OutboxWorker [%s] Error: Failed to fetch outbox batch: %v", w.eventType, err)
		return
	}
	if len(messages) == 0 {
		return
	}

	log.Printf("OutboxWorker [%s]: Processing batch of %d messages.", w.eventType, len(messages))

	for _, msg := range messages {
		var evt publisher.UserRegisteredEvent
		if err := json.Unmarshal(msg.Payload, &evt); err != nil {
			log.Printf("OutboxWorker [%s] Error: Bad payload for id='%s': %v", w.eventType, msg.ID, err)
			_ = w.outboxRepo.MarkFailed(ctx, msg.ID, err)
			continue
		}

		if pubErr := w.publisher.PublishUserRegistered(ctx, evt); pubErr != nil {
			log.Printf("OutboxWorker [%s] Warning: Publish failed for id='%s': %v", w.eventType, msg.ID, pubErr)
			_ = w.outboxRepo.MarkFailed(ctx, msg.ID, pubErr)
		} else {
			// BUG (Challenge 3): MarkPublished runs AFTER the message was published to RabbitMQ.
			// If the DB crashes or the network blips right here, the row stays PENDING and
			// will be re-published on the next poll — causing a duplicate delivery.
			if markErr := w.outboxRepo.MarkPublished(ctx, msg.ID); markErr != nil {
				log.Printf("OutboxWorker [%s] Error: MarkPublished failed for id='%s': %v", w.eventType, msg.ID, markErr)
			}
		}
	}
}
