package consumer

import (
	"context"
	"testing"

	"user-service/internal/service"
)

type mockTxManager struct {
	withTransactionFunc func(ctx context.Context, fn func(txCtx context.Context) error) error
}

func (m *mockTxManager) WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	if m.withTransactionFunc != nil {
		return m.withTransactionFunc(ctx, fn)
	}
	return fn(ctx)
}

type mockUserService struct {
	createUserFromWorkspaceFunc func(ctx context.Context, input service.CreateUserFromWorkspaceInput) error
}

func (m *mockUserService) CreateUserFromWorkspace(ctx context.Context, input service.CreateUserFromWorkspaceInput) error {
	if m.createUserFromWorkspaceFunc != nil {
		return m.createUserFromWorkspaceFunc(ctx, input)
	}
	return nil
}

type mockInboxRepository struct {
	tryInsertFunc func(ctx context.Context, eventID string) (bool, error)
}

func (m *mockInboxRepository) TryInsert(ctx context.Context, eventID string) (bool, error) {
	if m.tryInsertFunc != nil {
		return m.tryInsertFunc(ctx, eventID)
	}
	return false, nil
}

func TestWorkspaceInitiatedConsumer_UnitLogic(t *testing.T) {
	txManager := &mockTxManager{}
	inboxRepo := &mockInboxRepository{
		tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
			if eventID == "dup-1" {
				return true, nil // duplicate
			}
			return false, nil
		},
	}

	created := false
	userSvc := &mockUserService{
		createUserFromWorkspaceFunc: func(ctx context.Context, input service.CreateUserFromWorkspaceInput) error {
			created = true
			return nil
		},
	}

	// Test Duplicate Skip Flow
	err := txManager.WithTransaction(context.Background(), func(txCtx context.Context) error {
		isDup, err := inboxRepo.TryInsert(txCtx, "dup-1")
		if err != nil {
			return err
		}
		if isDup {
			return nil
		}
		return userSvc.CreateUserFromWorkspace(txCtx, service.CreateUserFromWorkspaceInput{})
	})

	if err != nil {
		t.Fatalf("expected no error on duplicate, got: %v", err)
	}
	if created {
		t.Error("expected user service NOT to be called for duplicate event")
	}

	// Test New Event Success Flow
	err = txManager.WithTransaction(context.Background(), func(txCtx context.Context) error {
		isDup, err := inboxRepo.TryInsert(txCtx, "new-1")
		if err != nil {
			return err
		}
		if isDup {
			return nil
		}
		return userSvc.CreateUserFromWorkspace(txCtx, service.CreateUserFromWorkspaceInput{})
	})

	if err != nil {
		t.Fatalf("expected no error on new event, got: %v", err)
	}
	if !created {
		t.Error("expected user service to be called for new event")
	}
}
