package worker

import (
	"context"
	"log/slog"
	"time"
)

type OutboxWorker struct {
	outboxRepository OutboxRepository
	publisher        EventPublisher
	pollInterval     time.Duration
	batchSize        int
	logger           *slog.Logger
	stopChan         chan struct{}
}

func NewOutboxWorker(
	outboxRepository OutboxRepository,
	publisher EventPublisher,
	pollInterval time.Duration,
	batchSize int,
	logger *slog.Logger,
) *OutboxWorker {
	if pollInterval <= 0 {
		pollInterval = 3 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 50
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxWorker{
		outboxRepository: outboxRepository,
		publisher:        publisher,
		pollInterval:     pollInterval,
		batchSize:        batchSize,
		logger:           logger,
		stopChan:         make(chan struct{}),
	}
}

func (outboxWorker *OutboxWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(outboxWorker.pollInterval)
	defer ticker.Stop()

	outboxWorker.logger.Info("starting payment outbox worker", "poll_interval", outboxWorker.pollInterval, "batch_size", outboxWorker.batchSize)

	for {
		select {
		case <-ctx.Done():
			outboxWorker.logger.Info("stopping payment outbox worker")
			return
		case <-outboxWorker.stopChan:
			outboxWorker.logger.Info("payment outbox worker stopped")
			return
		case <-ticker.C:
			outboxWorker.processOutboxBatch(ctx)
		}
	}
}

func (outboxWorker *OutboxWorker) Stop() {
	close(outboxWorker.stopChan)
}

func (outboxWorker *OutboxWorker) processOutboxBatch(ctx context.Context) {
	if err := outboxWorker.outboxRepository.RecoverStuckClaims(ctx); err != nil {
		outboxWorker.logger.Warn("failed to recover stuck outbox claims", "err", err)
	}

	messages, err := outboxWorker.outboxRepository.FetchPending(ctx, outboxWorker.batchSize)
	if err != nil {
		outboxWorker.logger.Error("failed to fetch outbox pending events", "err", err)
		return
	}

	if len(messages) == 0 {
		return
	}

	for _, msg := range messages {
		if err := outboxWorker.publisher.PublishEvent(ctx, msg.RoutingKey, msg.Payload); err != nil {
			outboxWorker.logger.Error("failed to publish outbox event", "event_id", msg.EventID, "routing_key", msg.RoutingKey, "err", err)
			_ = outboxWorker.outboxRepository.MarkFailed(ctx, msg.EventID, err.Error())
		} else {
			_ = outboxWorker.outboxRepository.MarkPublished(ctx, msg.EventID)
		}
	}
}
