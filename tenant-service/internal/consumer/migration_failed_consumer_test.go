package consumer

import (
	"context"
	"encoding/json"
	"testing"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/repository"
)

type mockTenantRepository struct {
	setTenantStatusFunc func(ctx context.Context, tenantID, status string) error
	setTenantStatusCall struct {
		tenantID string
		status   string
	}
	setCalled bool
}

func (m *mockTenantRepository) SetTenantStatus(ctx context.Context, tenantID, status string) error {
	m.setTenantStatusCall = struct {
		tenantID string
		status   string
	}{tenantID: tenantID, status: status}
	m.setCalled = true
	if m.setTenantStatusFunc != nil {
		return m.setTenantStatusFunc(ctx, tenantID, status)
	}
	return nil
}

type mockOutboxRepository struct {
	createOutboxMessageFunc func(ctx context.Context, input repository.CreateOutboxMessageInput) error
	createdInputs           []repository.CreateOutboxMessageInput
}

func (m *mockOutboxRepository) CreateOutboxMessage(ctx context.Context, input repository.CreateOutboxMessageInput) error {
	m.createdInputs = append(m.createdInputs, input)
	if m.createOutboxMessageFunc != nil {
		return m.createOutboxMessageFunc(ctx, input)
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

	t.Run("success_resets_status_and_stages_infrachanged", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxSvc := &mockInboxService{}
		tenantRepo := &mockTenantRepository{}
		outboxRepo := &mockOutboxRepository{}

		c := &MigrationFailedConsumer{
			txManager:        txManager,
			inboxService:     inboxSvc,
			tenantRepository: tenantRepo,
			outboxRepository: outboxRepo,
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
		if !tenantRepo.setCalled {
			t.Fatal("expected SetTenantStatus to be called")
		}
		if tenantRepo.setTenantStatusCall.tenantID != "tenant-99" || tenantRepo.setTenantStatusCall.status != domain.StatusActive {
			t.Errorf("expected status reset to '%s', got tenant='%s' status='%s'",
				domain.StatusActive, tenantRepo.setTenantStatusCall.tenantID, tenantRepo.setTenantStatusCall.status)
		}
		if len(outboxRepo.createdInputs) != 1 {
			t.Fatalf("expected 1 InfraChanged outbox message, got %d", len(outboxRepo.createdInputs))
		}
		if outboxRepo.createdInputs[0].EventType != domain.RoutingKeyInfraChanged {
			t.Errorf("expected InfraChanged outbox message, got event_type='%s'", outboxRepo.createdInputs[0].EventType)
		}
	})

	t.Run("duplicate_event_skips_side_effects", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxSvc := &mockInboxService{
			claimEventFunc: func(txCtx context.Context, eventID string) (bool, error) {
				return true, nil // duplicate
			},
		}
		tenantRepo := &mockTenantRepository{}
		outboxRepo := &mockOutboxRepository{}

		c := &MigrationFailedConsumer{
			txManager:        txManager,
			inboxService:     inboxSvc,
			tenantRepository: tenantRepo,
			outboxRepository: outboxRepo,
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
		if tenantRepo.setCalled {
			t.Error("expected no SetTenantStatus on duplicate")
		}
		if len(outboxRepo.createdInputs) != 0 {
			t.Error("expected no outbox message staged on duplicate")
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
			tenantRepository: &mockTenantRepository{},
			outboxRepository: &mockOutboxRepository{},
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