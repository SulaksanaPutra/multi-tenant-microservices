package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"user-service/internal/domain"
)

const (
	defaultDebounceDelay = 10 * time.Millisecond
	defaultPollInterval  = 5 * time.Second
	defaultBatchSize     = 50
)


type OutboxWorker struct {
	outboxRepository   OutboxRepository
	userEventPublisher UserEventPublisher
	wakeUpChan         chan struct{}
	debounceDelay      time.Duration
	pollInterval       time.Duration
	batchSize          int
}

func NewOutboxWorker(
	outboxRepository OutboxRepository,
	userEventPublisher UserEventPublisher,
) *OutboxWorker {
	return &OutboxWorker{
		outboxRepository:   outboxRepository,
		userEventPublisher: userEventPublisher,
		wakeUpChan:         make(chan struct{}, 1),
		debounceDelay:      defaultDebounceDelay,
		pollInterval:       defaultPollInterval,
		batchSize:          defaultBatchSize,
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

	w.processBatch(ctx, domain.RoutingKeyUserCreated)
}

func (w *OutboxWorker) recoverAndProcess(ctx context.Context) {
	for _, eventType := range []string{domain.RoutingKeyUserCreated} {
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
		case domain.RoutingKeyUserCreated:
			var evt domain.UserCreatedEvent
			if err := json.Unmarshal(msg.Payload, &evt); err != nil {
				log.Printf("OutboxWorker Error: Bad payload for id='%s': %v", msg.ID, err)
				_ = w.outboxRepository.MarkFailed(ctx, msg.ID, err)
				continue
			}
			pubErr = w.userEventPublisher.PublishUserCreated(ctx, evt)

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
