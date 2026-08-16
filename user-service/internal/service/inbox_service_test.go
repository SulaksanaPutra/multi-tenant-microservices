package service

import (
	"context"
	"errors"
	"testing"
)

type mockInboxRepository struct {
	tryInsertFunc func(ctx context.Context, eventID string) (bool, error)
}

func (m *mockInboxRepository) TryInsert(ctx context.Context, eventID string) (bool, error) {
	if m.tryInsertFunc != nil {
		return m.tryInsertFunc(ctx, eventID)
	}
	return false, nil
}

func TestInboxService_ClaimEvent(t *testing.T) {
	t.Run("empty_event_id_returns_false_without_repo_call", func(t *testing.T) {
		repoCalled := false
		mockInboxRepository := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				repoCalled = true
				return true, nil
			},
		}

		inboxService := NewInboxService(mockInboxRepository)
		isDup, err := inboxService.ClaimEvent(context.Background(), "")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if isDup {
			t.Error("expected isDup=false for empty event_id")
		}
		if repoCalled {
			t.Error("expected repository NOT to be called for empty event_id")
		}
	})

	t.Run("new_event_returns_false", func(t *testing.T) {
		var capturedID string
		mockInboxRepository := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				capturedID = eventID
				return false, nil // not duplicate
			},
		}

		inboxService := NewInboxService(mockInboxRepository)
		isDup, err := inboxService.ClaimEvent(context.Background(), "evt-456")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if isDup {
			t.Error("expected isDup=false for new event")
		}
		if capturedID != "evt-456" {
			t.Errorf("expected event_id='evt-456', got '%s'", capturedID)
		}
	})

	t.Run("duplicate_event_returns_true", func(t *testing.T) {
		mockInboxRepository := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				return true, nil // duplicate
			},
		}

		inboxService := NewInboxService(mockInboxRepository)
		isDup, err := inboxService.ClaimEvent(context.Background(), "evt-456")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !isDup {
			t.Error("expected isDup=true for duplicate event")
		}
	})

	t.Run("repository_error_returns_wrapped_error", func(t *testing.T) {
		dbErr := errors.New("db connection failure")
		mockInboxRepository := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				return false, dbErr
			},
		}

		inboxService := NewInboxService(mockInboxRepository)
		_, err := inboxService.ClaimEvent(context.Background(), "evt-456")
		if err == nil {
			t.Fatal("expected error when repository fails")
		}
		if !errors.Is(err, dbErr) {
			t.Errorf("expected error to wrap dbErr, got %v", err)
		}
	})
}
