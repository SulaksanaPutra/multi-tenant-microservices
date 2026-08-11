package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"

	"payment-service/internal/domain"
	"payment-service/internal/service"
)

type mockChannel struct {
	queueDeclareErr error
	queueBindErr    error
	consumeErr      error
	deliveryCh      chan amqp.Delivery
}

func (m *mockChannel) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error) {
	if m.queueDeclareErr != nil {
		return amqp.Queue{}, m.queueDeclareErr
	}
	return amqp.Queue{Name: name}, nil
}

func (m *mockChannel) QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error {
	return m.queueBindErr
}

func (m *mockChannel) Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error) {
	if m.consumeErr != nil {
		return nil, m.consumeErr
	}
	return m.deliveryCh, nil
}

type mockInitiator struct {
	initiateFn func(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error)
}

func (m *mockInitiator) InitiatePayment(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error) {
	if m.initiateFn != nil {
		return m.initiateFn(ctx, tenantID, orderID, amount, currency)
	}
	return &service.PaymentOutput{ID: "pay_1"}, nil
}

func TestOrderCreatedConsumer_SetupTopology(t *testing.T) {
	ch := &mockChannel{}
	svc := &mockInitiator{}
	c := NewOrderCreatedConsumer(ch, svc, nil)

	if err := c.SetupTopology(); err != nil {
		t.Fatalf("unexpected error in SetupTopology: %v", err)
	}

	ch.queueDeclareErr = errors.New("declare failed")
	if err := c.SetupTopology(); err == nil {
		t.Error("expected error when QueueDeclare fails")
	}
}

func TestOrderCreatedConsumer_HandleDelivery_UnmarshalError(t *testing.T) {
	ch := &mockChannel{}
	svc := &mockInitiator{}
	c := NewOrderCreatedConsumer(ch, svc, nil)

	// Delivery with invalid JSON payload
	d := amqp.Delivery{
		Body: []byte("invalid json"),
	}

	// Should not panic, logs unmarshal error and Nacks
	c.handleDelivery(context.Background(), d)
}

func TestOrderCreatedConsumer_HandleDelivery_Success(t *testing.T) {
	ch := &mockChannel{}
	var calledOrder string
	svc := &mockInitiator{
		initiateFn: func(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error) {
			calledOrder = orderID
			return &service.PaymentOutput{ID: "pay_test"}, nil
		},
	}
	c := NewOrderCreatedConsumer(ch, svc, nil)

	payload, _ := json.Marshal(domain.OrderCreatedEvent{
		EventID:  "evt_1",
		TenantID: "tnt_1",
		OrderID:  "ord_99",
		Amount:   50.0,
	})

	d := amqp.Delivery{
		Body: payload,
	}

	c.handleDelivery(context.Background(), d)
	if calledOrder != "ord_99" {
		t.Errorf("expected initiate for ord_99, got %q", calledOrder)
	}
}
