package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"time"

	"tenant-service/internal/publisher"
	"tenant-service/internal/repository"
)

const (
	defaultDebounceDelay = 10 * time.Millisecond
	defaultPollInterval  = 5 * time.Second
	defaultBatchSize     = 50
)

type ConnectionRegistry interface {
	GetAllActiveDedicatedPools() map[string]*sql.DB
}

type OutboxWorker struct {
	outboxRepo    repository.OutboxRepository
	publisher     publisher.TenantEventPublisher
	eventType     string
	registry      ConnectionRegistry
	wakeUpChan    chan struct{}
	debounceDelay time.Duration
	pollInterval  time.Duration
	batchSize     int
}

func NewOutboxWorker(
	outboxRepo repository.OutboxRepository,
	pub publisher.TenantEventPublisher,
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

func (w *OutboxWorker) SetConnectionRegistry(registry ConnectionRegistry) {
	w.registry = registry
}

// Poke sends a non-blocking wake-up signal to the worker loop.
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
			// Fallback sweep: also recovers stuck PROCESSING rows from crash scenarios. (Fix #1)
			w.recoverAndProcess(ctx)
		}
	}
}

// debounceAndProcess waits for the micro-delay window, draining concurrent pokes
// into a single batch execution. (Fix #3)
func (w *OutboxWorker) debounceAndProcess(ctx context.Context) {
	timer := time.NewTimer(w.debounceDelay)
	defer timer.Stop()

drainLoop:
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wakeUpChan:
			// Drain concurrent pokes arriving within the debounce window.
		case <-timer.C:
			break drainLoop
		}
	}

	w.processBatch(ctx)
}

// recoverAndProcess runs stuck-claim recovery before processing the batch. (Fix #1)
func (w *OutboxWorker) recoverAndProcess(ctx context.Context) {
	if err := w.outboxRepo.RecoverStuckClaims(ctx, w.eventType); err != nil {
		log.Printf("OutboxWorker [%s] Warning: Stuck-claim recovery failed: %v", w.eventType, err)
	}

	if w.registry != nil {
		for tenantID, pool := range w.registry.GetAllActiveDedicatedPools() {
			if err := w.outboxRepo.RecoverStuckClaimsFromDB(ctx, pool, w.eventType); err != nil {
				log.Printf("OutboxWorker [%s] Warning: Stuck-claim recovery failed for dedicated tenant %s: %v", w.eventType, tenantID, err)
			}
		}
	}

	w.processBatch(ctx)
}

// processBatch sweeps the primary DB outbox and all registered dedicated DB outboxes.
func (w *OutboxWorker) processBatch(ctx context.Context) {
	w.processBatchOnDB(ctx, nil)

	if w.registry != nil {
		for _, pool := range w.registry.GetAllActiveDedicatedPools() {
			w.processBatchOnDB(ctx, pool)
		}
	}
}

func (w *OutboxWorker) processBatchOnDB(ctx context.Context, targetDB *sql.DB) {
	var messages []repository.OutboxMessage
	var err error

	if targetDB == nil {
		messages, err = w.outboxRepo.FetchAndClaimBatch(ctx, w.eventType, w.batchSize)
	} else {
		messages, err = w.outboxRepo.FetchAndClaimBatchFromDB(ctx, targetDB, w.eventType, w.batchSize)
	}

	if err != nil {
		log.Printf("OutboxWorker [%s] Error: Failed to claim outbox batch: %v", w.eventType, err)
		return
	}
	if len(messages) == 0 {
		return
	}

	log.Printf("OutboxWorker [%s]: Processing batch of %d claimed messages.", w.eventType, len(messages))

	for _, msg := range messages {
		var evt publisher.TenantProvisionedEvent
		if err := json.Unmarshal(msg.Payload, &evt); err != nil {
			log.Printf("OutboxWorker [%s] Error: Bad payload for id='%s': %v", w.eventType, msg.ID, err)
			if targetDB == nil {
				_ = w.outboxRepo.MarkFailed(ctx, msg.ID, err)
			} else {
				_ = w.outboxRepo.MarkFailedOnDB(ctx, targetDB, msg.ID, err)
			}
			continue
		}

		if pubErr := w.publisher.PublishTenantProvisioned(ctx, evt); pubErr != nil {
			log.Printf("OutboxWorker [%s] Warning: Publish failed for id='%s': %v", w.eventType, msg.ID, pubErr)
			if targetDB == nil {
				_ = w.outboxRepo.MarkFailed(ctx, msg.ID, pubErr)
			} else {
				_ = w.outboxRepo.MarkFailedOnDB(ctx, targetDB, msg.ID, pubErr)
			}
		} else {
			if targetDB == nil {
				if markErr := w.outboxRepo.MarkPublished(ctx, msg.ID); markErr != nil {
					log.Printf("OutboxWorker [%s] Error: MarkPublished failed for id='%s': %v", w.eventType, msg.ID, markErr)
				}
			} else {
				if markErr := w.outboxRepo.MarkPublishedOnDB(ctx, targetDB, msg.ID); markErr != nil {
					log.Printf("OutboxWorker [%s] Error: MarkPublishedOnDB failed for id='%s': %v", w.eventType, msg.ID, markErr)
				}
			}
		}
	}

	// Re-poke if we filled the full batch — more rows likely remain. (Fix #3)
	if len(messages) == w.batchSize {
		log.Printf("OutboxWorker [%s]: Full batch processed — re-poking for remaining messages.", w.eventType)
		w.Poke()
	}
}
