package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"payment-service/internal/repository"
)

type mockWorkerOutboxRepo struct {
	fetchPendingFn  func(ctx context.Context, limit int) ([]*repository.OutboxMessage, error)
	markFailedFn    func(ctx context.Context, eventID string, reason string) error
	markPublishedFn func(ctx context.Context, eventID string) error
}

func (m *mockWorkerOutboxRepo) FetchPending(ctx context.Context, limit int) ([]*repository.OutboxMessage, error) {
	if m.fetchPendingFn != nil {
		return m.fetchPendingFn(ctx, limit)
	}
	return nil, nil
}

func (m *mockWorkerOutboxRepo) MarkFailed(ctx context.Context, eventID string, reason string) error {
	if m.markFailedFn != nil {
		return m.markFailedFn(ctx, eventID, reason)
	}
	return nil
}

func (m *mockWorkerOutboxRepo) MarkPublished(ctx context.Context, eventID string) error {
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
	repo := &mockWorkerOutboxRepo{}
	pub := &mockWorkerPublisher{}

	w := NewOutboxWorker(repo, pub, 0, 0, nil)
	if w == nil {
		t.Fatal("expected NewOutboxWorker to return non-nil worker pointer")
	}
	if w.pollInterval != 3*time.Second {
		t.Errorf("expected default pollInterval 3s, got %v", w.pollInterval)
	}
	if w.batchSize != 50 {
		t.Errorf("expected default batchSize 50, got %d", w.batchSize)
	}
}

func TestOutboxWorker_ProcessOutboxBatch_Success(t *testing.T) {
	publishedID := ""
	repo := &mockWorkerOutboxRepo{
		fetchPendingFn: func(ctx context.Context, limit int) ([]*repository.OutboxMessage, error) {
			return []*repository.OutboxMessage{
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

	pub := &mockWorkerPublisher{}
	w := NewOutboxWorker(repo, pub, 10*time.Millisecond, 10, nil)

	w.processOutboxBatch(context.Background())

	if publishedID != "evt-999" {
		t.Errorf("expected event evt-999 to be marked published, got %s", publishedID)
	}
}

func TestOutboxWorker_ProcessOutboxBatch_PublishFailure(t *testing.T) {
	failedID := ""
	repo := &mockWorkerOutboxRepo{
		fetchPendingFn: func(ctx context.Context, limit int) ([]*repository.OutboxMessage, error) {
			return []*repository.OutboxMessage{
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

	pub := &mockWorkerPublisher{
		publishFn: func(ctx context.Context, routingKey string, payload []byte) error {
			return errors.New("amqp publish connection error")
		},
	}

	w := NewOutboxWorker(repo, pub, 10*time.Millisecond, 10, nil)
	w.processOutboxBatch(context.Background())

	if failedID != "evt-888" {
		t.Errorf("expected event evt-888 to be marked failed, got %s", failedID)
	}
}
