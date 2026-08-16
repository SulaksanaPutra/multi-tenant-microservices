package publisher

import (
	"context"
	"testing"
	"time"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/rabbitmq"
)

func TestUserPublisher_Constructor_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	userPublisher, err := NewUserPublisher(client)
	if err == nil {
		t.Fatalf("expected error when declaring exchange on uninitialized channel, got nil")
	}
	if userPublisher != nil {
		t.Fatalf("expected UserPublisher pointer to be nil on constructor failure, got %v", userPublisher)
	}
}

func TestUserPublisher_PublishUserCreated_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	userPublisher := &UserPublisher{client: client}

	evt := domain.UserCreatedEvent{
		EventID:   "evt-123",
		UserID:    "usr-456",
		TenantID:  "tenant-789",
		Email:     "user@example.com",
		Name:      "John Doe",
		CreatedAt: time.Now().UTC(),
	}

	err := userPublisher.PublishUserCreated(context.Background(), evt)
	if err == nil {
		t.Fatalf("expected error publishing UserCreated event with uninitialized channel, got nil")
	}
}

func TestUserPublisher_PublishUserCreated_ContextCancelled(t *testing.T) {
	client := &rabbitmq.Client{}
	userPublisher := &UserPublisher{client: client}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	evt := domain.UserCreatedEvent{
		EventID:  "evt-999",
		UserID:   "usr-888",
		TenantID: "tenant-777",
	}

	err := userPublisher.PublishUserCreated(ctx, evt)
	if err == nil {
		t.Fatalf("expected error when context is cancelled, got nil")
	}
}

func TestUserPublisher_StructInitialization(t *testing.T) {
	client := &rabbitmq.Client{}
	userPublisher := &UserPublisher{client: client}
	if userPublisher.client != client {
		t.Fatalf("expected client reference to match")
	}
}
