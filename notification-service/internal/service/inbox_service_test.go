package service

import (
	"context"
	"errors"
	"testing"

	"notification-service/internal/domain"
	"notification-service/internal/repository"
)

type mockInboxRepository struct {
	tryInsertFunc            func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error)
	listEventsByTenantIDFunc func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error)
	acquireTenantLockFunc    func(ctx context.Context, tenantID string) error
}

func (m *mockInboxRepository) TryInsert(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
	if m.tryInsertFunc != nil {
		return m.tryInsertFunc(ctx, input)
	}
	return false, nil
}

func (m *mockInboxRepository) ListEventsByTenantID(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
	if m.listEventsByTenantIDFunc != nil {
		return m.listEventsByTenantIDFunc(ctx, tenantID)
	}
	return []domain.InboxMessage{}, nil
}

func (m *mockInboxRepository) AcquireTenantLock(ctx context.Context, tenantID string) error {
	if m.acquireTenantLockFunc != nil {
		return m.acquireTenantLockFunc(ctx, tenantID)
	}
	return nil
}

func TestInboxService_ClaimEvent_EmptyEventID(t *testing.T) {
	inboxService := NewInboxService(&mockInboxRepository{})
	isDup, err := inboxService.ClaimEvent(context.Background(), ClaimInboxInput{EventID: ""})
	if err != nil {
		t.Fatalf("expected no error for empty event_id, got %v", err)
	}
	if isDup {
		t.Error("expected isDuplicate=false for empty event_id")
	}
}

func TestInboxService_ClaimEvent_NewEvent(t *testing.T) {
	inboxService := NewInboxService(&mockInboxRepository{
		tryInsertFunc: func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return false, nil // not a duplicate
		},
	})
	isDup, err := inboxService.ClaimEvent(context.Background(), ClaimInboxInput{
		EventID:   "evt-new",
		TenantID:  "tenant-1",
		EventType: "user.created",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isDup {
		t.Error("expected isDuplicate=false for a new event")
	}
}

func TestInboxService_ClaimEvent_DuplicateEvent(t *testing.T) {
	inboxService := NewInboxService(&mockInboxRepository{
		tryInsertFunc: func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return true, nil // duplicate
		},
	})
	isDup, err := inboxService.ClaimEvent(context.Background(), ClaimInboxInput{
		EventID:   "evt-dup",
		TenantID:  "tenant-1",
		EventType: "user.created",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isDup {
		t.Error("expected isDuplicate=true for a duplicate event")
	}
}

func TestInboxService_ClaimEvent_TryInsertError(t *testing.T) {
	expectedErr := errors.New("db connection lost")
	inboxService := NewInboxService(&mockInboxRepository{
		tryInsertFunc: func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return false, expectedErr
		},
	})
	_, err := inboxService.ClaimEvent(context.Background(), ClaimInboxInput{
		EventID:  "evt-fail",
		TenantID: "tenant-1",
	})
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected wrapped error %v, got %v", expectedErr, err)
	}
}

func TestInboxService_ListBarrierEvents_ReturnsEvents(t *testing.T) {
	mockInboxRepository := &mockInboxRepository{
		listEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
			return []domain.InboxMessage{{EventID: "evt-1"}}, nil
		},
	}
	inboxService := NewInboxService(mockInboxRepository)

	events, err := inboxService.ListBarrierEvents(context.Background(), "t-1")
	if err != nil || len(events) != 1 {
		t.Fatalf("expected 1 event, got err=%v events=%v", err, events)
	}
}

func TestInboxService_ListBarrierEvents_RepoError(t *testing.T) {
	mockInboxRepository := &mockInboxRepository{
		listEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
			return nil, errors.New("db error")
		},
	}
	inboxService := NewInboxService(mockInboxRepository)

	_, err := inboxService.ListBarrierEvents(context.Background(), "t-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
