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
	defaultDebounceDelay = 10 * time.Millisecond
	defaultPollInterval  = 5 * time.Second
	defaultBatchSize     = 50
)

type OutboxWorker struct {
	outboxRepo    repository.OutboxRepository
	publisher     publisher.UserEventPublisher
	eventType     string
	wakeUpChan    chan struct{}
	debounceDelay time.Duration
	pollInterval  time.Duration
	batchSize     int
}

func NewOutboxWorker(
	outboxRepo repository.OutboxRepository,
	pub publisher.UserEventPublisher,
	eventType string,
) *OutboxWorker {
	return &OutboxWorker{
		outboxRepo:    outboxRepo,
		publisher:     pub,
		eventType:     eventType,
		wakeUpChan:    make(chan struct{}, 1),
		debounceDelay: defaultDebounceDelay,
		pollInterval:  defaultPollInterval,
		batchSize:     defaultBatchSize,
	}
}

// Poke sends a non-blocking wake-up signal to the worker loop.
// If the worker is already awake (channel buffer full), the signal is safely dropped —
// the worker will process the newly staged messages in its current or next cycle.
func (w *OutboxWorker) Poke() {
	select {
	case w.wakeUpChan <- struct{}{}:
	default:
	}
}

func (w *OutboxWorker) Start(ctx context.Context) {
	log.Printf("OutboxWorker [%s]: Started (debounce=%v, batch=%d, poll=%v)",
		w.eventType, w.debounceDelay, w.batchSize, w.pollInterval)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("OutboxWorker [%s]: Shutting down.", w.eventType)
			return
		case <-w.wakeUpChan:
			w.debounceAndProcess(ctx)
		case <-ticker.C:
			// Fallback sweep: also recovers stuck PROCESSING rows from crash scenarios.
			w.recoverAndProcess(ctx)
		}
	}
}

// debounceAndProcess waits for the micro-delay window, draining any concurrent pokes
// into a single batch execution. This prevents N insertions from causing N wake-ups. (Fix #3)
func (w *OutboxWorker) debounceAndProcess(ctx context.Context) {
	timer := time.NewTimer(w.debounceDelay)
	defer timer.Stop()

drainLoop:
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wakeUpChan:
			// Drain any concurrent pokes arriving in the debounce window.
		case <-timer.C:
			break drainLoop
		}
	}

	w.processBatch(ctx)
}

// recoverAndProcess runs stuck-claim recovery before processing.
// This handles the case where a worker instance died mid-publish. (Fix #1)
func (w *OutboxWorker) recoverAndProcess(ctx context.Context) {
	if err := w.outboxRepo.RecoverStuckClaims(ctx, w.eventType); err != nil {
		log.Printf("OutboxWorker [%s] Warning: Stuck-claim recovery failed: %v", w.eventType, err)
	}
	w.processBatch(ctx)
}

// processBatch atomically claims a batch, publishes each message, and updates status.
// If the batch was full, it re-pokes itself to drain remaining PENDING messages
// without waiting for the next ticker cycle. (Fix #3)
func (w *OutboxWorker) processBatch(ctx context.Context) {
	messages, err := w.outboxRepo.FetchAndClaimBatch(ctx, w.eventType, w.batchSize)
	if err != nil {
		log.Printf("OutboxWorker [%s] Error: Failed to claim outbox batch: %v", w.eventType, err)
		return
	}
	if len(messages) == 0 {
		return
	}

	log.Printf("OutboxWorker [%s]: Processing batch of %d claimed messages.", w.eventType, len(messages))

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
			if markErr := w.outboxRepo.MarkPublished(ctx, msg.ID); markErr != nil {
				log.Printf("OutboxWorker [%s] Error: MarkPublished failed for id='%s': %v", w.eventType, msg.ID, markErr)
			}
		}
	}

	// If we filled the entire batch, there are likely more PENDING rows.
	// Re-poke to chain the next batch immediately without waiting for the ticker. (Fix #3)
	if len(messages) == w.batchSize {
		log.Printf("OutboxWorker [%s]: Full batch processed — re-poking for remaining messages.", w.eventType)
		w.Poke()
	}
}
