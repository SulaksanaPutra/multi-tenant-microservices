package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"payment-service/internal/domain"
)

type mockWorkerOutboxRepository struct {
	fetchPendingFn       func(ctx context.Context, limit int) ([]*domain.OutboxMessage, error)
	recoverStuckClaimsFn func(ctx context.Context) error
	markFailedFn         func(ctx context.Context, eventID string, reason string) error
	markPublishedFn      func(ctx context.Context, eventID string) error
}

func (m *mockWorkerOutboxRepository) ListPending(ctx context.Context, limit int) ([]*domain.OutboxMessage, error) {
	if m.fetchPendingFn != nil {
		return m.fetchPendingFn(ctx, limit)
	}
	return nil, nil
}

func (m *mockWorkerOutboxRepository) RecoverStuckClaims(ctx context.Context) error {
	if m.recoverStuckClaimsFn != nil {
		return m.recoverStuckClaimsFn(ctx)
	}
	return nil
}

func (m *mockWorkerOutboxRepository) MarkFailed(ctx context.Context, eventID string, reason string) error {
	if m.markFailedFn != nil {
		return m.markFailedFn(ctx, eventID, reason)
	}
	return nil
}

func (m *mockWorkerOutboxRepository) MarkPublished(ctx context.Context, eventID string) error {
	if m.markPublishedFn != nil {
		return m.markPublishedFn(ctx, eventID)
	}
	return nil
}

type mockWorkerPublisher struct {
	publishFn func(ctx context.Context, routingKey string, payload []byte) error
}

func (m *mockWorkerPublisher) PublishEvent(ctx context.Context, routingKey string, payload []byte) error {
	if m.publishFn != nil {
		return m.publishFn(ctx, routingKey, payload)
	}
	return nil
}

func TestOutboxWorker_ConstructorDefaults(t *testing.T) {
	outboxRepository := &mockWorkerOutboxRepository{}
	workerPublisher := &mockWorkerPublisher{}

	outboxWorker := NewOutboxWorker(outboxRepository, workerPublisher, 0, 0, nil)
	if outboxWorker == nil {
		t.Fatal("expected NewOutboxWorker to return non-nil worker pointer")
	}
	if outboxWorker.pollInterval != 3*time.Second {
		t.Errorf("expected default pollInterval 3s, got %v", outboxWorker.pollInterval)
	}
	if outboxWorker.batchSize != 50 {
		t.Errorf("expected default batchSize 50, got %d", outboxWorker.batchSize)
	}
}

func TestOutboxWorker_ProcessOutboxBatch_Success(t *testing.T) {
	publishedID := ""
	outboxRepository := &mockWorkerOutboxRepository{
		fetchPendingFn: func(ctx context.Context, limit int) ([]*domain.OutboxMessage, error) {
			return []*domain.OutboxMessage{
				{
					EventID:    "evt-999",
					RoutingKey: "payment.created",
					Payload:    []byte(`{"status":"SUCCESS"}`),
				},
			}, nil
		},
		markPublishedFn: func(ctx context.Context, eventID string) error {
			publishedID = eventID
			return nil
		},
	}

	workerPublisher := &mockWorkerPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, workerPublisher, 10*time.Millisecond, 10, nil)

	outboxWorker.processOutboxBatch(context.Background())

	if publishedID != "evt-999" {
		t.Errorf("expected event evt-999 to be marked published, got %s", publishedID)
	}
}

func TestOutboxWorker_ProcessOutboxBatch_PublishFailure(t *testing.T) {
	failedID := ""
	outboxRepository := &mockWorkerOutboxRepository{
		fetchPendingFn: func(ctx context.Context, limit int) ([]*domain.OutboxMessage, error) {
			return []*domain.OutboxMessage{
				{
					EventID:    "evt-888",
					RoutingKey: "payment.failed",
					Payload:    []byte(`{}`),
				},
			}, nil
		},
		markFailedFn: func(ctx context.Context, eventID string, reason string) error {
			failedID = eventID
			return nil
		},
	}

	workerPublisher := &mockWorkerPublisher{
		publishFn: func(ctx context.Context, routingKey string, payload []byte) error {
			return errors.New("amqp publish connection error")
		},
	}

	outboxWorker := NewOutboxWorker(outboxRepository, workerPublisher, 10*time.Millisecond, 10, nil)
	outboxWorker.processOutboxBatch(context.Background())

	if failedID != "evt-888" {
		t.Errorf("expected event evt-888 to be marked failed, got %s", failedID)
	}
}
