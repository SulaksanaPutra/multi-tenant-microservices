package publisher

import (
	"context"
	"testing"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/rabbitmq"
)

func TestTenantPublisher_Constructor_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	pub, err := NewTenantPublisher(client)
	if err == nil {
		t.Fatalf("expected error when declaring exchange on uninitialized channel, got nil")
	}
	if pub != nil {
		t.Fatalf("expected TenantPublisher pointer to be nil on constructor failure, got %v", pub)
	}
}

func TestTenantPublisher_PublishWorkspaceInitiated_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	pub := &TenantPublisher{client: client}

	evt := domain.WorkspaceInitiatedEvent{
		EventID:    "evt-101",
		TenantID:   "tenant-101",
		Plan:       "PRO",
		OwnerEmail: "owner@company.com",
		OwnerName:  "Jane Owner",
	}

	err := pub.PublishWorkspaceInitiated(context.Background(), evt)
	if err == nil {
		t.Fatalf("expected error publishing WorkspaceInitiated event with uninitialized channel, got nil")
	}
}

func TestTenantPublisher_PublishWorkspaceInitiated_ContextCancelled(t *testing.T) {
	client := &rabbitmq.Client{}
	pub := &TenantPublisher{client: client}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	evt := domain.WorkspaceInitiatedEvent{
		EventID:  "evt-102",
		TenantID: "tenant-102",
	}

	err := pub.PublishWorkspaceInitiated(ctx, evt)
	if err == nil {
		t.Fatalf("expected error when context is cancelled, got nil")
	}
}

func TestTenantPublisher_PublishWorkspaceReady_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	pub := &TenantPublisher{client: client}

	evt := domain.WorkspaceReadyEvent{
		EventID:    "evt-201",
		TenantID:   "tenant-201",
		OwnerEmail: "owner@company.com",
	}

	err := pub.PublishWorkspaceReady(context.Background(), evt)
	if err == nil {
		t.Fatalf("expected error publishing WorkspaceReady event with uninitialized channel, got nil")
	}
}

func TestTenantPublisher_PublishWorkspaceReady_ContextCancelled(t *testing.T) {
	client := &rabbitmq.Client{}
	pub := &TenantPublisher{client: client}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	evt := domain.WorkspaceReadyEvent{
		EventID:  "evt-202",
		TenantID: "tenant-202",
	}

	err := pub.PublishWorkspaceReady(ctx, evt)
	if err == nil {
		t.Fatalf("expected error when context is cancelled, got nil")
	}
}

func TestTenantPublisher_StructInitialization(t *testing.T) {
	client := &rabbitmq.Client{}
	pub := &TenantPublisher{client: client}
	if pub == nil {
		t.Fatal("expected TenantPublisher struct pointer to be non-nil")
	}
	if pub.client != client {
		t.Fatalf("expected client reference to match")
	}
}
