package service

import (
	"context"
	"errors"
	"testing"

	"payment-service/internal/repository"
)

type mockInboxRepository struct {
	tryInsertFn      func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error)
	saveInboxEventFn func(ctx context.Context, eventID string, eventType string) error
}

func (m *mockInboxRepository) TryInsert(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
	if m.tryInsertFn != nil {
		return m.tryInsertFn(ctx, input)
	}
	return false, nil
}

func (m *mockInboxRepository) SaveInboxEvent(ctx context.Context, eventID string, eventType string) error {
	if m.saveInboxEventFn != nil {
		return m.saveInboxEventFn(ctx, eventID, eventType)
	}
	return nil
}

func TestInboxService_ClaimEvent(t *testing.T) {
	t.Run("empty event ID returns not duplicate without DB call", func(t *testing.T) {
		inboxRepository := &mockInboxRepository{
			tryInsertFn: func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
				t.Fatal("should not call DB for empty event_id")
				return false, nil
			},
		}
		inboxService := NewInboxService(inboxRepository)
		isDup, err := inboxService.ClaimEvent(context.Background(), ClaimInboxInput{EventID: ""})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if isDup {
			t.Error("expected isDup to be false for empty eventID")
		}
	})

	t.Run("new event claims successfully", func(t *testing.T) {
		inboxRepository := &mockInboxRepository{
			tryInsertFn: func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
				if input.EventID != "evt_123" {
					t.Errorf("expected EventID 'evt_123', got '%s'", input.EventID)
				}
				return false, nil
			},
		}
		inboxService := NewInboxService(inboxRepository)
		isDup, err := inboxService.ClaimEvent(context.Background(), ClaimInboxInput{
			EventID:   "evt_123",
			TenantID:  "tenant_1",
			EventType: "order.created",
			Payload:   []byte(`{"order_id":"ord_1"}`),
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if isDup {
			t.Error("expected isDup to be false")
		}
	})

	t.Run("duplicate event returns isDuplicate true", func(t *testing.T) {
		inboxRepository := &mockInboxRepository{
			tryInsertFn: func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
				return true, nil
			},
		}
		inboxService := NewInboxService(inboxRepository)
		isDup, err := inboxService.ClaimEvent(context.Background(), ClaimInboxInput{
			EventID: "evt_dup",
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !isDup {
			t.Error("expected isDup to be true")
		}
	})

	t.Run("repository error propagates", func(t *testing.T) {
		inboxRepository := &mockInboxRepository{
			tryInsertFn: func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
				return false, errors.New("db connection lost")
			},
		}
		inboxService := NewInboxService(inboxRepository)
		_, err := inboxService.ClaimEvent(context.Background(), ClaimInboxInput{
			EventID: "evt_err",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
