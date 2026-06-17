package consumer

import (
	"context"
	"encoding/json"
	"testing"

	"notification-service/internal/domain"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
)

func TestOrderCreatedConsumer_HandleDelivery(t *testing.T) {
	validEvt := domain.OrderCreatedEvent{
		EventID:    "evt-order-1",
		TenantID:   "tenant-7",
		OrderID:    "order-1",
		CustomerID: "customer-1",
		Amount:     42.5,
		Status:     "PENDING",
	}
	validBody, _ := json.Marshal(validEvt)

	t.Run("success_claims_inbox_and_acks", func(t *testing.T) {
		txManager := &mockTxManager{}
		var claimedInput service.ClaimInboxInput
		inboxSvc := &mockInboxService{
			claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
				claimedInput = input
				return false, nil
			},
		}

		c := &OrderCreatedConsumer{
			txManager:    txManager,
			inboxService: inboxSvc,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
			RoutingKey:   domain.RoutingKeyOrderCreated,
		}

		if err := c.handleDelivery(context.Background(), d); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected message to be ACKed")
		}
		if claimedInput.EventID != "evt-order-1" {
			t.Errorf("expected inbox claim for event 'evt-order-1', got '%s'", claimedInput.EventID)
		}
	})

	t.Run("duplicate_event_is_skipped_and_acked", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxSvc := &mockInboxService{
			claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
				return true, nil // duplicate
			},
		}

		c := &OrderCreatedConsumer{
			txManager:    txManager,
			inboxService: inboxSvc,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
			RoutingKey:   domain.RoutingKeyOrderCreated,
		}

		if err := c.handleDelivery(context.Background(), d); err != nil {
			t.Fatalf("expected no error for duplicate, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected duplicate message to be ACKed")
		}
	})

	t.Run("invalid_json_nacks_without_requeue", func(t *testing.T) {
		c := &OrderCreatedConsumer{
			txManager:    &mockTxManager{},
			inboxService: &mockInboxService{},
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         []byte("invalid-json"),
			RoutingKey:   domain.RoutingKeyOrderCreated,
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

		c := &OrderCreatedConsumer{
			txManager:    txManager,
			inboxService: &mockInboxService{},
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
			RoutingKey:   domain.RoutingKeyOrderCreated,
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