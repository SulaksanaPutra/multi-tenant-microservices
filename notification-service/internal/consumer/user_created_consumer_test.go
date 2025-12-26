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

type mockTxManager struct {
	withTransactionFunc func(ctx context.Context, fn func(txCtx context.Context) error) error
}

func (m *mockTxManager) WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	if m.withTransactionFunc != nil {
		return m.withTransactionFunc(ctx, fn)
	}
	return fn(ctx)
}

type mockNotificationService struct {
	processEventAndTrySendWelcomeFunc func(ctx context.Context, input service.ProcessEventInput) error
}

func (m *mockNotificationService) ProcessEventAndTrySendWelcome(ctx context.Context, input service.ProcessEventInput) error {
	if m.processEventAndTrySendWelcomeFunc != nil {
		return m.processEventAndTrySendWelcomeFunc(ctx, input)
	}
	return nil
}

type mockAcknowledger struct {
	ackCalled   bool
	nackCalled  bool
	requeueVal  bool
	multipleVal bool
}

func (m *mockAcknowledger) Ack(tag uint64, multiple bool) error {
	m.ackCalled = true
	m.multipleVal = multiple
	return nil
}

func (m *mockAcknowledger) Nack(tag uint64, multiple, requeue bool) error {
	m.nackCalled = true
	m.multipleVal = multiple
	m.requeueVal = requeue
	return nil
}

func (m *mockAcknowledger) Reject(tag uint64, requeue bool) error {
	return nil
}

func TestUserCreatedConsumer_HandleDelivery(t *testing.T) {
	evt := domain.UserCreatedEvent{
		EventID:  "evt-user-1",
		UserID:   "usr_100",
		TenantID: "tenant-99",
		Email:    "john@example.com",
		Name:     "John Doe",
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

		c := &UserCreatedConsumer{
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
		if capturedInput.EventID != "evt-user-1" || capturedInput.UserID != "usr_100" || capturedInput.OwnerEmail != "john@example.com" {
			t.Errorf("unexpected input passed to service: %+v", capturedInput)
		}
	})

	t.Run("invalid_json_nacks_without_requeue", func(t *testing.T) {
		c := &UserCreatedConsumer{}
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
		svcErr := errors.New("mail delivery temporary failure")
		notifSvc := &mockNotificationService{
			processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput) error {
				return svcErr
			},
		}

		c := &UserCreatedConsumer{
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
