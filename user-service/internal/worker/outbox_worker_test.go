package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"user-service/internal/domain"
)

type mockOutboxRepo struct {
	recoverStuckClaimsFunc func(ctx context.Context, eventType string) error
	fetchAndClaimBatchFunc func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error)
	markFailedFunc         func(ctx context.Context, id string, err error) error
	markPublishedFunc      func(ctx context.Context, id string) error
}

func (m *mockOutboxRepo) RecoverStuckClaims(ctx context.Context, eventType string) error {
	if m.recoverStuckClaimsFunc != nil {
		return m.recoverStuckClaimsFunc(ctx, eventType)
	}
	return nil
}

func (m *mockOutboxRepo) FetchAndClaimBatch(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
	if m.fetchAndClaimBatchFunc != nil {
		return m.fetchAndClaimBatchFunc(ctx, eventType, limit)
	}
	return nil, nil
}

func (m *mockOutboxRepo) MarkFailed(ctx context.Context, id string, err error) error {
	if m.markFailedFunc != nil {
		return m.markFailedFunc(ctx, id, err)
	}
	return nil
}

func (m *mockOutboxRepo) MarkPublished(ctx context.Context, id string) error {
	if m.markPublishedFunc != nil {
		return m.markPublishedFunc(ctx, id)
	}
	return nil
}

type mockUserEventPublisher struct {
	publishUserCreatedFunc func(ctx context.Context, evt domain.UserCreatedEvent) error
}

func (m *mockUserEventPublisher) PublishUserCreated(ctx context.Context, evt domain.UserCreatedEvent) error {
	if m.publishUserCreatedFunc != nil {
		return m.publishUserCreatedFunc(ctx, evt)
	}
	return nil
}

func TestOutboxWorker_ProcessBatch_Success(t *testing.T) {
	evt := domain.UserCreatedEvent{
		EventID:  "evt-1",
		UserID:   "usr_123456",
		TenantID: "tenant-1",
		Email:    "test@example.com",
	}
	payload, _ := json.Marshal(evt)

	publishedIDs := make([]string, 0)

	repo := &mockOutboxRepo{
		fetchAndClaimBatchFunc: func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
			return []domain.OutboxMessage{
				{
					ID:        "evt-1",
					EventType: domain.RoutingKeyUserCreated,
					Payload:   payload,
				},
			}, nil
		},
		markPublishedFunc: func(ctx context.Context, id string) error {
			publishedIDs = append(publishedIDs, id)
			return nil
		},
	}

	pub := &mockUserEventPublisher{}

	worker := NewOutboxWorker(repo, pub)
	worker.processBatch(context.Background(), domain.RoutingKeyUserCreated)

	if len(publishedIDs) != 1 || publishedIDs[0] != "evt-1" {
		t.Errorf("expected msg 'evt-1' to be marked as published, got %v", publishedIDs)
	}
}

func TestOutboxWorker_ProcessBatch_PublishError(t *testing.T) {
	evt := domain.UserCreatedEvent{
		EventID: "evt-2",
	}
	payload, _ := json.Marshal(evt)

	failedIDs := make([]string, 0)

	repo := &mockOutboxRepo{
		fetchAndClaimBatchFunc: func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
			return []domain.OutboxMessage{
				{
					ID:        "evt-2",
					EventType: domain.RoutingKeyUserCreated,
					Payload:   payload,
				},
			}, nil
		},
		markFailedFunc: func(ctx context.Context, id string, err error) error {
			failedIDs = append(failedIDs, id)
			return nil
		},
	}

	pub := &mockUserEventPublisher{
		publishUserCreatedFunc: func(ctx context.Context, evt domain.UserCreatedEvent) error {
			return errors.New("rabbit disconnect")
		},
	}

	worker := NewOutboxWorker(repo, pub)
	worker.processBatch(context.Background(), domain.RoutingKeyUserCreated)

	if len(failedIDs) != 1 || failedIDs[0] != "evt-2" {
		t.Errorf("expected msg 'evt-2' to be marked as failed, got %v", failedIDs)
	}
}

func TestOutboxWorker_Poke(t *testing.T) {
	worker := NewOutboxWorker(&mockOutboxRepo{}, &mockUserEventPublisher{})
	worker.Poke()
	// Consecutive non-blocking poke should not block
	worker.Poke()
}
