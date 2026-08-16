package publisher

import (
	"context"
	"testing"

	"infra-provisioner/internal/domain"
	"infra-provisioner/internal/infrastructure/rabbitmq"
)

func TestInfrastructurePublisher_Constructor_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	infrastructurePublisher, err := NewInfrastructurePublisher(client)
	if err == nil {
		t.Fatalf("expected error when declaring exchange on uninitialized channel, got nil")
	}
	if infrastructurePublisher != nil {
		t.Fatalf("expected InfrastructurePublisher pointer to be nil on constructor failure, got %v", infrastructurePublisher)
	}
}

func TestInfrastructurePublisher_PublishInfrastructureProvisioned_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	infrastructurePublisher := &InfrastructurePublisher{client: client}

	event := domain.InfrastructureProvisionedEvent{
		EventID:    "evt-401",
		TenantID:   "tenant-401",
		Plan:       "PRO",
		DBHost:     "localhost",
		DBPort:     5432,
		DBName:     "app_db",
		DBUser:     "postgres",
		SchemaName: "tenant_401",
	}

	err := infrastructurePublisher.PublishInfrastructureProvisioned(context.Background(), event)
	if err == nil {
		t.Fatalf("expected error publishing InfrastructureProvisioned event with uninitialized channel, got nil")
	}
}

func TestInfrastructurePublisher_PublishInfrastructureProvisioned_ContextCancelled(t *testing.T) {
	client := &rabbitmq.Client{}
	infrastructurePublisher := &InfrastructurePublisher{client: client}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	event := domain.InfrastructureProvisionedEvent{
		EventID:  "evt-402",
		TenantID: "tenant-402",
	}

	err := infrastructurePublisher.PublishInfrastructureProvisioned(ctx, event)
	if err == nil {
		t.Fatalf("expected error when context is cancelled, got nil")
	}
}

func TestInfrastructurePublisher_StructInitialization(t *testing.T) {
	client := &rabbitmq.Client{}
	infrastructurePublisher := &InfrastructurePublisher{client: client}
	if infrastructurePublisher.client != client {
		t.Fatalf("expected client reference to match")
	}
}
