package service

import (
	"context"
	"errors"
	"testing"

	"auth-service/internal/repository"
)

type mockInboxRepo struct {
	tryInsertFunc func(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error)
}

func (m *mockInboxRepo) TryInsert(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
	if m.tryInsertFunc != nil {
		return m.tryInsertFunc(ctx, input)
	}
	return false, nil
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
