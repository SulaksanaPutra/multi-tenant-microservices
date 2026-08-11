package worker

import (
	"context"
	"testing"
	"time"

	"payment-service/internal/repository"
)

type mockOutboxRepo struct{}

func (m *mockOutboxRepo) FetchPending(ctx context.Context, limit int) ([]*repository.OutboxMessage, error) {
	return nil, nil
}

func (m *mockOutboxRepo) MarkFailed(ctx context.Context, eventID string, reason string) error {
	return nil
}

func (m *mockOutboxRepo) MarkPublished(ctx context.Context, eventID string) error {
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
	repo := &mockOutboxRepo{}
	pub := &mockPublisher{}

	w := NewOutboxWorker(repo, pub, 10*time.Millisecond, 10, nil)
	if w == nil {
		t.Fatal("expected NewOutboxWorker to return non-nil worker")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	w.Stop()
}

func TestExpirationSweeper_NewAndStop(t *testing.T) {
	swp := &mockSweeper{}
	e := NewExpirationSweeper(swp, 10*time.Millisecond, 1*time.Hour, nil)
	if e == nil {
		t.Fatal("expected NewExpirationSweeper to return non-nil sweeper")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go e.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	e.Stop()
}
