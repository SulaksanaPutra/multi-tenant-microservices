package publisher

import (
	"context"
	"testing"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/rabbitmq"
)

func TestTenantPublisher_NilChannel(t *testing.T) {
	client := &rabbitmq.Client{}
	pub := &TenantPublisher{client: client}

	err := pub.PublishWorkspaceInitiated(context.Background(), domain.WorkspaceInitiatedEvent{TenantID: "tenant-1"})
	if err == nil {
		t.Fatalf("expected error publishing with nil channel, got nil")
	}

	err = pub.PublishWorkspaceReady(context.Background(), domain.WorkspaceReadyEvent{TenantID: "tenant-1"})
	if err == nil {
		t.Fatalf("expected error publishing with nil channel, got nil")
	}
}
