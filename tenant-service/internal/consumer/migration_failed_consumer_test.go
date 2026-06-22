package consumer

import (
	"context"
	"encoding/json"
	"testing"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/rabbitmq"
)

type mockMigrationRollbackService struct {
	rollbackFunc func(ctx context.Context, tenantID string) error
	called       bool
	tenantID     string
}

func (m *mockMigrationRollbackService) RollbackFailedMigration(ctx context.Context, tenantID string) error {
	m.called = true
	m.tenantID = tenantID
	if m.rollbackFunc != nil {
		return m.rollbackFunc(ctx, tenantID)
	}
	return nil
}

func TestMigrationFailedConsumer_HandleDelivery(t *testing.T) {
	validEvt := domain.TenantMigrationFailedEvent{
		EventID:  "evt-mig-fail-1",
		TenantID: "tenant-99",
		Reason:   "pg_dump failed",
	}
	validBody, _ := json.Marshal(validEvt)

	t.Run("success_invokes_rollback_service", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxService := &mockInboxService{}
		rollbackService := &mockMigrationRollbackService{}

		c := &MigrationFailedConsumer{
			txManager:                txManager,
			inboxService:             inboxService,
			migrationRollbackService: rollbackService,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		if err := c.handleDelivery(context.Background(), d); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected message to be ACKed")
		}
		if !rollbackService.called {
			t.Fatal("expected RollbackFailedMigration to be called")
		}
		if rollbackService.tenantID != "tenant-99" {
			t.Errorf("expected rollback for tenant 'tenant-99', got '%s'", rollbackService.tenantID)
		}
	})

	t.Run("duplicate_event_skips_side_effects", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxService := &mockInboxService{
			claimEventFunc: func(txCtx context.Context, eventID string) (bool, error) {
				return true, nil // duplicate
			},
		}
		rollbackService := &mockMigrationRollbackService{}

		c := &MigrationFailedConsumer{
			txManager:                txManager,
			inboxService:             inboxService,
			migrationRollbackService: rollbackService,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		if err := c.handleDelivery(context.Background(), d); err != nil {
			t.Fatalf("expected no error for duplicate, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected duplicate message to be ACKed")
		}
		if rollbackService.called {
			t.Error("expected no RollbackFailedMigration on duplicate")
		}
	})

	t.Run("invalid_json_nacks_without_requeue", func(t *testing.T) {
		c := &MigrationFailedConsumer{}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         []byte("invalid-json"),
		}

		if err := c.handleDelivery(context.Background(), d); err == nil {
			t.Error("expected json unmarshal error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed")
		}
		if mockAck.requeueVal {
			t.Error("expected requeue=false for bad JSON payload")
		}
	})

	t.Run("tx_failure_nacks_with_requeue", func(t *testing.T) {
		txManager := &mockTxManager{
			withTransactionFunc: func(ctx context.Context, fn func(txCtx context.Context) error) error {
				return context.DeadlineExceeded
			},
		}

		c := &MigrationFailedConsumer{
			txManager: txManager,
			inboxService: &mockInboxService{
				claimEventFunc: func(txCtx context.Context, eventID string) (bool, error) {
					return false, nil
				},
			},
			migrationRollbackService: &mockMigrationRollbackService{},
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		if err := c.handleDelivery(context.Background(), d); err == nil {
			t.Error("expected transaction error to propagate")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed on tx failure")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true on transient tx failure")
		}
	})
}
