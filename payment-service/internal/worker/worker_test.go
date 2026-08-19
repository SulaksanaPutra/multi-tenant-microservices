package worker

import (
	"context"
	"testing"
	"time"

	"payment-service/internal/domain"
)

type mockOutboxRepository struct{}

func (m *mockOutboxRepository) FetchPending(ctx context.Context, limit int) ([]*domain.OutboxMessage, error) {
	return nil, nil
}

func (m *mockOutboxRepository) RecoverStuckClaims(ctx context.Context) error {
	return nil
}

func (m *mockOutboxRepository) MarkFailed(ctx context.Context, eventID string, reason string) error {
	return nil
}

func (m *mockOutboxRepository) MarkPublished(ctx context.Context, eventID string) error {
	return nil
}

type mockPublisher struct{}

func (m *mockPublisher) PublishEvent(ctx context.Context, routingKey string, payload []byte) error {
	return nil
}

type mockSweeper struct{}

func (m *mockSweeper) SweepExpiredPayments(ctx context.Context, ttl time.Duration) (int, error) {
	return 0, nil
}

func TestOutboxWorker_NewAndStop(t *testing.T) {
	outboxRepository := &mockOutboxRepository{}
	workerPublisher := &mockPublisher{}

	outboxWorker := NewOutboxWorker(outboxRepository, workerPublisher, 10*time.Millisecond, 10, nil)
	if outboxWorker == nil {
		t.Fatal("expected NewOutboxWorker to return non-nil worker")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go outboxWorker.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	outboxWorker.Stop()
}

func TestExpirationSweeper_NewAndStop(t *testing.T) {
	paymentSweeper := &mockSweeper{}
	expirationSweeper := NewExpirationSweeper(paymentSweeper, 10*time.Millisecond, 1*time.Hour, nil)
	if expirationSweeper == nil {
		t.Fatal("expected NewExpirationSweeper to return non-nil sweeper")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go expirationSweeper.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	expirationSweeper.Stop()
}
