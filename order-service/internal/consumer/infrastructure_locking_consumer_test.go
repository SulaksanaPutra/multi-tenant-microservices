package consumer

import (
	"context"
	"encoding/json"
	"testing"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
)

func TestInfrastructureLockingConsumer_HandleDelivery(t *testing.T) {
	validEvt := domain.InfrastructureLockingEvent{
		EventID:  "evt-lock-1",
		TenantID: "tenant-42",
	}
	validBody, _ := json.Marshal(validEvt)

	t.Run("success_sets_migrating_status", func(t *testing.T) {
		routingReg := registry.NewRoutingRegistry()
		routingReg.Set(registry.RoutingMetadata{TenantID: "tenant-42", DBHost: "postgres", SchemaName: "tenant_42_order_db"})

		c := &InfrastructureLockingConsumer{
			routingRegistry: routingReg,
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
		if got := routingReg.GetStatus("tenant-42"); got != "MIGRATING" {
			t.Errorf("expected status MIGRATING, got '%s'", got)
		}
	})

	t.Run("creates_minimal_entry_when_tenant_absent", func(t *testing.T) {
		routingReg := registry.NewRoutingRegistry()

		c := &InfrastructureLockingConsumer{
			routingRegistry: routingReg,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		if err := c.handleDelivery(context.Background(), d); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if got := routingReg.GetStatus("tenant-42"); got != "MIGRATING" {
			t.Errorf("expected status MIGRATING on minimal entry, got '%s'", got)
		}
	})

	t.Run("invalid_json_nacks_without_requeue", func(t *testing.T) {
		c := &InfrastructureLockingConsumer{
			routingRegistry: registry.NewRoutingRegistry(),
		}

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

	t.Run("missing_tenant_id_nacks_without_requeue", func(t *testing.T) {
		evt := domain.InfrastructureLockingEvent{EventID: "evt-lock-2"}
		body, _ := json.Marshal(evt)

		c := &InfrastructureLockingConsumer{
			routingRegistry: registry.NewRoutingRegistry(),
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         body,
		}

		if err := c.handleDelivery(context.Background(), d); err == nil {
			t.Error("expected missing tenant_id error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed")
		}
		if mockAck.requeueVal {
			t.Error("expected requeue=false for missing tenant_id")
		}
	})
}