package consumer

import (
	"context"
	"encoding/json"
	"testing"

	"notification-service/internal/domain"
	"notification-service/internal/service"
)

func TestWorkspaceReadyConsumer_ProcessLogic(t *testing.T) {
	txManager := &mockTxManager{}

	evt := domain.WorkspaceReadyEvent{
		EventID:    "evt-ws-1",
		TenantID:   "tenant-88",
		OwnerEmail: "owner@company.com",
	}
	body, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}

	var capturedInput service.ProcessEventInput

	notifSvc := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput) error {
			capturedInput = input
			return nil
		},
	}

	err = txManager.WithTransaction(context.Background(), func(txCtx context.Context) error {
		var unmarshaled domain.WorkspaceReadyEvent
		if err := json.Unmarshal(body, &unmarshaled); err != nil {
			return err
		}
		input := service.ProcessEventInput{
			EventID:    unmarshaled.EventID,
			TenantID:   unmarshaled.TenantID,
			EventType:  domain.RoutingKeyWorkspaceReady,
			OwnerEmail: unmarshaled.OwnerEmail,
			Payload:    body,
		}
		return notifSvc.ProcessEventAndTrySendWelcome(txCtx, input)
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if capturedInput.EventID != "evt-ws-1" || capturedInput.TenantID != "tenant-88" || capturedInput.OwnerEmail != "owner@company.com" {
		t.Errorf("unexpected input captured for WorkspaceReadyConsumer: %+v", capturedInput)
	}
}
