package service

import (
	"context"
	"errors"
	"testing"

	"user-service/internal/domain"
)

type mockUserRepository struct {
	createUserFunc func(ctx context.Context, user domain.User) error
}

func (m *mockUserRepository) CreateUser(ctx context.Context, user domain.User) error {
	if m.createUserFunc != nil {
		return m.createUserFunc(ctx, user)
	}
	return nil
}

type mockOutboxRepository struct {
	createOutboxMessageFunc func(ctx context.Context, msg domain.OutboxMessage) error
}

func (m *mockOutboxRepository) CreateOutboxMessage(ctx context.Context, msg domain.OutboxMessage) error {
	if m.createOutboxMessageFunc != nil {
		return m.createOutboxMessageFunc(ctx, msg)
	}
	return nil
}

func TestUserService_CreateUserFromWorkspace_Success(t *testing.T) {
	var createdUser domain.User
	var createdOutbox domain.OutboxMessage

	userRepo := &mockUserRepository{
		createUserFunc: func(ctx context.Context, user domain.User) error {
			createdUser = user
			return nil
		},
	}

	outboxRepo := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, msg domain.OutboxMessage) error {
			createdOutbox = msg
			return nil
		},
	}

	svc := NewUserService(userRepo, outboxRepo)

	input := CreateUserFromWorkspaceInput{
		EventID:    "evt-123",
		TenantID:   "tenant-456",
		OwnerEmail: "owner@company.com",
		OwnerName:  "Alice Smith",
	}

	err := svc.CreateUserFromWorkspace(context.Background(), input)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if createdUser.Email != "owner@company.com" || createdUser.Name != "Alice Smith" {
		t.Errorf("unexpected user record created: %+v", createdUser)
	}

	if createdOutbox.AggregateID != createdUser.ID || createdOutbox.EventType != domain.RoutingKeyUserCreated {
		t.Errorf("unexpected outbox message created: %+v", createdOutbox)
	}
}

func TestUserService_CreateUserFromWorkspace_UserRepoError(t *testing.T) {
	expectedErr := errors.New("db error")
	userRepo := &mockUserRepository{
		createUserFunc: func(ctx context.Context, user domain.User) error {
			return expectedErr
		},
	}

	svc := NewUserService(userRepo, nil)

	input := CreateUserFromWorkspaceInput{
		OwnerEmail: "test@example.com",
	}

	err := svc.CreateUserFromWorkspace(context.Background(), input)
	if err == nil || !errors.Is(err, expectedErr) {
		t.Errorf("expected error wrapping '%v', got '%v'", expectedErr, err)
	}
}

func TestUserService_CreateUserFromWorkspace_OutboxRepoError(t *testing.T) {
	userRepo := &mockUserRepository{}
	expectedErr := errors.New("outbox write failure")
	outboxRepo := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, msg domain.OutboxMessage) error {
			return expectedErr
		},
	}

	svc := NewUserService(userRepo, outboxRepo)

	input := CreateUserFromWorkspaceInput{
		OwnerEmail: "test@example.com",
	}

	err := svc.CreateUserFromWorkspace(context.Background(), input)
	if err == nil || !errors.Is(err, expectedErr) {
		t.Errorf("expected error wrapping '%v', got '%v'", expectedErr, err)
	}
}
