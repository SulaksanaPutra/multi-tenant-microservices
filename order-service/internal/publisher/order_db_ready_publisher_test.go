package publisher

import (
	"context"
	"testing"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
)

func TestOrderDBReadyPublisher_Constructor_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	orderDBReadyPublisher, err := NewOrderDBReadyPublisher(client)
	if err == nil {
		t.Fatalf("expected error when declaring exchange on uninitialized channel, got nil")
	}
	if orderDBReadyPublisher != nil {
		t.Fatalf("expected OrderDBReadyPublisher pointer to be nil on constructor failure, got %v", orderDBReadyPublisher)
	}
}

func TestOrderDBReadyPublisher_PublishTenantOrderDBReady_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	orderDBReadyPublisher := &OrderDBReadyPublisher{client: client}

	evt := domain.TenantOrderDBReadyEvent{
		EventID:     "evt-301",
		TenantID:    "tenant-301",
		ServiceName: "order-service",
		DBHost:      "localhost",
		DBPort:      5432,
		DBName:      "order_db",
		DBUser:      "postgres",
		SchemaName:  "tenant_301",
	}

	err := orderDBReadyPublisher.PublishTenantOrderDBReady(context.Background(), evt)
	if err == nil {
		t.Fatalf("expected error publishing TenantOrderDBReady event with uninitialized channel, got nil")
	}
}

func TestOrderDBReadyPublisher_PublishTenantOrderDBReady_ContextCancelled(t *testing.T) {
	client := &rabbitmq.Client{}
	orderDBReadyPublisher := &OrderDBReadyPublisher{client: client}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	evt := domain.TenantOrderDBReadyEvent{
		EventID:  "evt-302",
		TenantID: "tenant-302",
	}

	err := orderDBReadyPublisher.PublishTenantOrderDBReady(ctx, evt)
	if err == nil {
		t.Fatalf("expected error when context is cancelled, got nil")
	}
}

func TestOrderDBReadyPublisher_StructInitialization(t *testing.T) {
	client := &rabbitmq.Client{}
	orderDBReadyPublisher := &OrderDBReadyPublisher{client: client}
	if orderDBReadyPublisher.client != client {
		t.Fatalf("expected client reference to match")
	}
}
