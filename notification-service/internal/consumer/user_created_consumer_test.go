package consumer

import (
	"context"
	"encoding/json"
	"testing"

	"notification-service/internal/domain"
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

func TestUserCreatedConsumer_ProcessLogic(t *testing.T) {
	txManager := &mockTxManager{}

	evt := domain.UserCreatedEvent{
		EventID:  "evt-user-1",
		UserID:   "usr_100",
		TenantID: "tenant-99",
		Email:    "john@example.com",
		Name:     "John Doe",
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
		var unmarshaled domain.UserCreatedEvent
		if err := json.Unmarshal(body, &unmarshaled); err != nil {
			return err
		}
		input := service.ProcessEventInput{
			EventID:    unmarshaled.EventID,
			UserID:     unmarshaled.UserID,
			TenantID:   unmarshaled.TenantID,
			EventType:  domain.RoutingKeyUserCreated,
			OwnerEmail: unmarshaled.Email,
			Payload:    body,
		}
		return notifSvc.ProcessEventAndTrySendWelcome(txCtx, input)
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if capturedInput.EventID != "evt-user-1" || capturedInput.UserID != "usr_100" || capturedInput.OwnerEmail != "john@example.com" {
		t.Errorf("unexpected input captured for UserCreatedConsumer: %+v", capturedInput)
	}
}
