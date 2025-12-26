package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"notification-service/internal/domain"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
)

func TestWorkspaceReadyConsumer_HandleDelivery(t *testing.T) {
	evt := domain.WorkspaceReadyEvent{
		EventID:    "evt-ws-1",
		TenantID:   "tenant-88",
		OwnerEmail: "owner@company.com",
	}
	body, _ := json.Marshal(evt)

	t.Run("success_event", func(t *testing.T) {
		txManager := &mockTxManager{}
		var capturedInput service.ProcessEventInput
		notifSvc := &mockNotificationService{
			processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput) error {
				capturedInput = input
				return nil
			},
		}

		c := &WorkspaceReadyConsumer{
			txManager:           txManager,
			notificationService: notifSvc,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         body,
		}

		err := c.handleDelivery(context.Background(), d)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected message to be ACKed")
		}
		if capturedInput.EventID != "evt-ws-1" || capturedInput.TenantID != "tenant-88" || capturedInput.OwnerEmail != "owner@company.com" {
			t.Errorf("unexpected input passed to service: %+v", capturedInput)
		}
	})

	t.Run("invalid_json_nacks_without_requeue", func(t *testing.T) {
		c := &WorkspaceReadyConsumer{}
		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         []byte("invalid-json"),
		}

		err := c.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected json unmarshal error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed")
		}
		if mockAck.requeueVal {
			t.Error("expected requeue=false for bad JSON payload")
		}
	})

	t.Run("service_error_nacks_with_requeue", func(t *testing.T) {
		txManager := &mockTxManager{}
		svcErr := errors.New("mail sending failed")
		notifSvc := &mockNotificationService{
			processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput) error {
				return svcErr
			},
		}

		c := &WorkspaceReadyConsumer{
			txManager:           txManager,
			notificationService: notifSvc,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         body,
		}

		err := c.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected error when notification service fails")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed on service error")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true for transient service error")
		}
	})
}
