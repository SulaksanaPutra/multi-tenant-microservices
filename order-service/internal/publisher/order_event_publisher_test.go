package publisher

import (
	"context"
	"testing"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
)

func TestOrderEventPublisher_Constructor_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	orderEventPublisher, err := NewOrderEventPublisher(client)
	if err == nil {
		t.Fatalf("expected error when declaring exchange on uninitialized channel, got nil")
	}
	if orderEventPublisher != nil {
		t.Fatalf("expected OrderEventPublisher pointer to be nil on constructor failure, got %v", orderEventPublisher)
	}
}

func TestOrderEventPublisher_PublishOrderCreated_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	orderEventPublisher := &OrderEventPublisher{client: client}

	evt := domain.OrderCreatedEvent{
		EventID:  "evt-101",
		TenantID: "tenant-101",
		OrderID:  "order-101",
	}

	err := orderEventPublisher.PublishOrderCreated(context.Background(), evt)
	if err == nil {
		t.Fatalf("expected error publishing OrderCreated event with uninitialized channel, got nil")
	}
}

func TestOrderEventPublisher_PublishOrderCreated_ContextCancelled(t *testing.T) {
	client := &rabbitmq.Client{}
	orderEventPublisher := &OrderEventPublisher{client: client}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	evt := domain.OrderCreatedEvent{
		EventID:  "evt-102",
		TenantID: "tenant-102",
		OrderID:  "order-102",
	}

	err := orderEventPublisher.PublishOrderCreated(ctx, evt)
	if err == nil {
		t.Fatalf("expected error when context is cancelled, got nil")
	}
}

func TestOrderEventPublisher_StructInitialization(t *testing.T) {
	client := &rabbitmq.Client{}
	orderEventPublisher := &OrderEventPublisher{client: client}
	if orderEventPublisher.client != client {
		t.Fatalf("expected client reference to match")
	}
}
