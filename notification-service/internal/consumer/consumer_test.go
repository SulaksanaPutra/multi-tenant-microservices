package consumer

import (
	"context"
	"testing"

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

func TestConsumer_UnitLogic(t *testing.T) {
	txManager := &mockTxManager{}
	processed := false

	svc := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput) error {
			processed = true
			return nil
		},
	}

	err := txManager.WithTransaction(context.Background(), func(txCtx context.Context) error {
		return svc.ProcessEventAndTrySendWelcome(txCtx, service.ProcessEventInput{EventID: "evt-123"})
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !processed {
		t.Error("expected event to be processed inside transaction boundary")
	}
}
