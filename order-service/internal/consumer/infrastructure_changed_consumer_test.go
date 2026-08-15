package consumer

import (
	"context"
	"encoding/json"
	"testing"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
)

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

func TestInfrastructureChangedConsumer_HandleDelivery(t *testing.T) {
	poolReg := registry.NewPoolRegistry()
	routingReg := registry.NewRoutingRegistry()

	c := &InfrastructureChangedConsumer{
		poolRegistry:    poolReg,
		routingRegistry: routingReg,
	}

	evt := domain.InfraChangedEvent{
		TenantID: "tenant-change-1",
	}
	body, _ := json.Marshal(evt)

	t.Run("success_evicts_cache_and_acks", func(t *testing.T) {
		// Populate registry first
		routingReg.Set(registry.RoutingMetadata{TenantID: "tenant-change-1", DBHost: "host1"})

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

		// Verify routing registry entry was evicted
		if _, ok := routingReg.Get("tenant-change-1"); ok {
			t.Error("expected tenant-change-1 to be deleted from routing registry")
		}
	})

	t.Run("invalid_json_nacks_without_requeue", func(t *testing.T) {
		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         []byte("bad-json"),
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
}

func TestNewInfrastructureChangedConsumer(t *testing.T) {
	c := NewInfrastructureChangedConsumer(InfrastructureChangedConsumerParams{
		Client:          &mockAMQPInterfaceClient{},
		PoolRegistry:    registry.NewPoolRegistry(),
		RoutingRegistry: registry.NewRoutingRegistry(),
	})
	if c == nil {
		t.Fatal("expected non-nil consumer")
	}
}
