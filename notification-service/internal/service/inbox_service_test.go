package service

import (
	"context"
	"errors"
	"testing"

	"notification-service/internal/domain"
	"notification-service/internal/repository"
)

type mockInboxRepo struct {
	tryInsertFunc           func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error)
	getEventsByTenantIDFunc func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error)
	acquireTenantLockFunc   func(ctx context.Context, tenantID string) error
}

func (m *mockInboxRepo) TryInsert(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
	if m.tryInsertFunc != nil {
		return m.tryInsertFunc(ctx, input)
	}
	return false, nil
}

func (m *mockInboxRepo) GetEventsByTenantID(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
	if m.getEventsByTenantIDFunc != nil {
		return m.getEventsByTenantIDFunc(ctx, tenantID)
	}
	return []domain.InboxMessage{}, nil
}

func (m *mockInboxRepo) AcquireTenantLock(ctx context.Context, tenantID string) error {
	if m.acquireTenantLockFunc != nil {
		return m.acquireTenantLockFunc(ctx, tenantID)
	}
	return nil
}

func TestInboxService_ClaimEvent_EmptyEventID(t *testing.T) {
	svc := NewInboxService(&mockInboxRepo{})
	isDup, err := svc.ClaimEvent(context.Background(), ClaimInboxInput{EventID: ""})
	if err != nil {
		t.Fatalf("expected no error for empty event_id, got %v", err)
	}
	if isDup {
		t.Error("expected isDuplicate=false for empty event_id")
	}
}

func TestInboxService_ClaimEvent_NewEvent(t *testing.T) {
	svc := NewInboxService(&mockInboxRepo{
		tryInsertFunc: func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return false, nil // not a duplicate
		},
	})
	isDup, err := svc.ClaimEvent(context.Background(), ClaimInboxInput{
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
	svc := NewInboxService(&mockInboxRepo{
		tryInsertFunc: func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return true, nil // duplicate
		},
	})
	isDup, err := svc.ClaimEvent(context.Background(), ClaimInboxInput{
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
	svc := NewInboxService(&mockInboxRepo{
		tryInsertFunc: func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return false, expectedErr
		},
	})
	_, err := svc.ClaimEvent(context.Background(), ClaimInboxInput{
		EventID:  "evt-fail",
		TenantID: "tenant-1",
	})
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected wrapped error %v, got %v", expectedErr, err)
	}
}

func TestInboxService_GetBarrierEvents_ReturnsEvents(t *testing.T) {
	expected := []domain.InboxMessage{
		{EventID: "e-1", TenantID: "t-1", EventType: "user.created"},
		{EventID: "e-2", TenantID: "t-1", EventType: "workspace.ready"},
	}
	svc := NewInboxService(&mockInboxRepo{
		getEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
			return expected, nil
		},
	})
	events, err := svc.GetBarrierEvents(context.Background(), "t-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestInboxService_GetBarrierEvents_RepoError(t *testing.T) {
	expectedErr := errors.New("db timeout")
	svc := NewInboxService(&mockInboxRepo{
		getEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
			return nil, expectedErr
		},
	})
	_, err := svc.GetBarrierEvents(context.Background(), "t-1")
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected wrapped error %v, got %v", expectedErr, err)
	}
}
