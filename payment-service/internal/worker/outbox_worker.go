package worker

import (
	"context"
	"log/slog"
	"time"

	"payment-service/internal/repository"
)

type EventPublisher interface {
	PublishEvent(ctx context.Context, routingKey string, payload []byte) error
}

type OutboxRepository interface {
	FetchPending(ctx context.Context, limit int) ([]*repository.OutboxMessage, error)
	MarkFailed(ctx context.Context, eventID string, reason string) error
	MarkPublished(ctx context.Context, eventID string) error
}

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

func (w *OutboxWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	w.logger.Info("starting payment outbox worker", "poll_interval", w.pollInterval, "batch_size", w.batchSize)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("stopping payment outbox worker")
			return
		case <-w.stopChan:
			w.logger.Info("payment outbox worker stopped")
			return
		case <-ticker.C:
			w.processOutboxBatch(ctx)
		}
	}
}

func (w *OutboxWorker) Stop() {
	close(w.stopChan)
}

func (w *OutboxWorker) processOutboxBatch(ctx context.Context) {
	messages, err := w.outboxRepository.FetchPending(ctx, w.batchSize)
	if err != nil {
		w.logger.Error("failed to fetch outbox pending events", "err", err)
		return
	}

	if len(messages) == 0 {
		return
	}

	for _, msg := range messages {
		if err := w.publisher.PublishEvent(ctx, msg.RoutingKey, msg.Payload); err != nil {
			w.logger.Error("failed to publish outbox event", "event_id", msg.EventID, "routing_key", msg.RoutingKey, "err", err)
			_ = w.outboxRepository.MarkFailed(ctx, msg.EventID, err.Error())
		} else {
			_ = w.outboxRepository.MarkPublished(ctx, msg.EventID)
		}
	}
}
