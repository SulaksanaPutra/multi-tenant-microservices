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
		mockRepo := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				repoCalled = true
				return true, nil
			},
		}

		svc := NewInboxService(mockRepo)
		isDup, err := svc.ClaimEvent(context.Background(), "")
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
		mockRepo := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				capturedID = eventID
				return false, nil // not duplicate
			},
		}

		svc := NewInboxService(mockRepo)
		isDup, err := svc.ClaimEvent(context.Background(), "evt-123")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if isDup {
			t.Error("expected isDup=false for new event")
		}
		if capturedID != "evt-123" {
			t.Errorf("expected event_id='evt-123', got '%s'", capturedID)
		}
	})

	t.Run("duplicate_event_returns_true", func(t *testing.T) {
		mockRepo := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				return true, nil // duplicate
			},
		}

		svc := NewInboxService(mockRepo)
		isDup, err := svc.ClaimEvent(context.Background(), "evt-123")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !isDup {
			t.Error("expected isDup=true for duplicate event")
		}
	})

	t.Run("repository_error_returns_wrapped_error", func(t *testing.T) {
		dbErr := errors.New("db connection timeout")
		mockRepo := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				return false, dbErr
			},
		}

		svc := NewInboxService(mockRepo)
		_, err := svc.ClaimEvent(context.Background(), "evt-123")
		if err == nil {
			t.Fatal("expected error when repository fails")
		}
		if !errors.Is(err, dbErr) {
			t.Errorf("expected error to wrap dbErr, got %v", err)
		}
	})
}
