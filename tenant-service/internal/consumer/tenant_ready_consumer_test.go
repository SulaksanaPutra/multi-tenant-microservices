package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/service"
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

type mockInboxService struct {
	claimEventFunc func(txCtx context.Context, eventID string) (bool, error)
}

func (m *mockInboxService) ClaimEvent(txCtx context.Context, eventID string) (bool, error) {
	if m.claimEventFunc != nil {
		return m.claimEventFunc(txCtx, eventID)
	}
	return false, nil
}

type mockTenantInfrastructureService struct {
	handleInfrastructureUpdateFunc func(ctx context.Context, input service.InfrastructureUpdateInput) error
}

func (m *mockTenantInfrastructureService) HandleInfrastructureUpdate(ctx context.Context, input service.InfrastructureUpdateInput) error {
	if m.handleInfrastructureUpdateFunc != nil {
		return m.handleInfrastructureUpdateFunc(ctx, input)
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

func TestTenantOrderDBReadyConsumer_HandleDelivery(t *testing.T) {
	validEvt := domain.TenantOrderDBReadyEvent{
		EventID:     "evt-ready-1",
		TenantID:    "tenant-100",
		ServiceName: "order-service",
		DBHost:      "localhost",
		DBPort:      5432,
		DBName:      "shared_db",
		DBUser:      "postgres",
		SchemaName:  "tenant_100_order_db",
	}
	validBody, _ := json.Marshal(validEvt)

	t.Run("success_with_inbox_check", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxService := &mockInboxService{
			claimEventFunc: func(txCtx context.Context, eventID string) (bool, error) {
				return false, nil // not duplicate
			},
		}

		var capturedInput service.InfrastructureUpdateInput
		tenantInfrastructureService := &mockTenantInfrastructureService{
			handleInfrastructureUpdateFunc: func(ctx context.Context, input service.InfrastructureUpdateInput) error {
				capturedInput = input
				return nil
			},
		}

		tenantOrderDBReadyConsumer := &TenantOrderDBReadyConsumer{
			txManager:                   txManager,
			inboxService:                inboxService,
			tenantInfrastructureService: tenantInfrastructureService,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		err := tenantOrderDBReadyConsumer.handleDelivery(context.Background(), d)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected message to be ACKed")
		}
		if capturedInput.TenantID != "tenant-100" || capturedInput.ServiceName != "order-service" {
			t.Errorf("unexpected input passed to infra update service: %+v", capturedInput)
		}
	})

	t.Run("duplicate_event_skips_processing", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxService := &mockInboxService{
			claimEventFunc: func(txCtx context.Context, eventID string) (bool, error) {
				return true, nil // duplicate
			},
		}

		serviceCalled := false
		tenantInfrastructureService := &mockTenantInfrastructureService{
			handleInfrastructureUpdateFunc: func(ctx context.Context, input service.InfrastructureUpdateInput) error {
				serviceCalled = true
				return nil
			},
		}

		tenantOrderDBReadyConsumer := &TenantOrderDBReadyConsumer{
			txManager:                   txManager,
			inboxService:                inboxService,
			tenantInfrastructureService: tenantInfrastructureService,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		err := tenantOrderDBReadyConsumer.handleDelivery(context.Background(), d)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected duplicate message to be ACKed")
		}
		if serviceCalled {
			t.Error("expected infra service NOT to be called on duplicate event")
		}
	})

	t.Run("pure_constructor_initialization", func(t *testing.T) {
		tenantOrderDBReadyConsumer := NewTenantOrderDBReadyConsumer(TenantOrderDBReadyConsumerParams{
			TxManager:                   &mockTxManager{},
			Client:                      &mockAMQPInterfaceClient{},
			TenantInfrastructureService: &mockTenantInfrastructureService{},
			InboxService:                &mockInboxService{},
		})
		if tenantOrderDBReadyConsumer == nil {
			t.Fatal("expected non-nil consumer")
		}
	})

	t.Run("invalid_json_nacks_without_requeue", func(t *testing.T) {
		tenantOrderDBReadyConsumer := &TenantOrderDBReadyConsumer{}
		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         []byte("invalid-json"),
		}

		err := tenantOrderDBReadyConsumer.handleDelivery(context.Background(), d)
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
		serviceErr := errors.New("infra update failed")
		tenantInfrastructureService := &mockTenantInfrastructureService{
			handleInfrastructureUpdateFunc: func(ctx context.Context, input service.InfrastructureUpdateInput) error {
				return serviceErr
			},
		}

		tenantOrderDBReadyConsumer := &TenantOrderDBReadyConsumer{
			txManager:                   txManager,
			inboxService:                &mockInboxService{},
			tenantInfrastructureService: tenantInfrastructureService,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		err := tenantOrderDBReadyConsumer.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected error when service fails")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed on service error")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true for transient service error")
		}
	})

	t.Run("max_delivery_count_nacks_without_requeue_for_dlq", func(t *testing.T) {
		tenantOrderDBReadyConsumer := &TenantOrderDBReadyConsumer{
			txManager:                   &mockTxManager{},
			inboxService:                &mockInboxService{},
			tenantInfrastructureService: &mockTenantInfrastructureService{},
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
			Headers: map[string]interface{}{
				"x-delivery-count": 3,
			},
		}

		if err := tenantOrderDBReadyConsumer.handleDelivery(context.Background(), d); err == nil {
			t.Error("expected max delivery count error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed")
		}
		if mockAck.requeueVal {
			t.Error("expected requeue=false for DLQ")
		}
	})
}

func TestNewTenantOrderDBReadyConsumer(t *testing.T) {
	tenantOrderDBReadyConsumer := NewTenantOrderDBReadyConsumer(TenantOrderDBReadyConsumerParams{
		TxManager:                   &mockTxManager{},
		Client:                      &mockAMQPInterfaceClient{},
		TenantInfrastructureService: &mockTenantInfrastructureService{},
		InboxService:                &mockInboxService{},
	})
	if tenantOrderDBReadyConsumer == nil {
		t.Fatal("expected non-nil consumer")
	}
}
