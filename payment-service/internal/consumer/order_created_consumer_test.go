package consumer

import (
	"context"
	"encoding/json"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"

	"payment-service/internal/domain"
	"payment-service/internal/service"
)

type mockAMQPClient struct {
	connCtx context.Context
	ch      *amqp.Channel
}

func (m *mockAMQPClient) ConnContext() context.Context {
	if m.connCtx != nil {
		return m.connCtx
	}
	return context.Background()
}

func (m *mockAMQPClient) WaitUntilReady(ctx context.Context) error {
	return nil
}

func (m *mockAMQPClient) DeclareExchange(name, kind string) error {
	return nil
}

func (m *mockAMQPClient) GetChannel() *amqp.Channel {
	return m.ch
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

func TestOrderCreatedConsumer_HandleDelivery_UnmarshalError(t *testing.T) {
	client := &mockAMQPClient{}
	svc := &mockInitiator{}
	c := NewOrderCreatedConsumer(client, svc, nil)

	// Delivery with invalid JSON payload
	d := amqp.Delivery{
		Body: []byte("invalid json"),
	}

	// Should not panic, logs unmarshal error and Nacks
	c.handleDelivery(context.Background(), d)
}

func TestOrderCreatedConsumer_HandleDelivery_Success(t *testing.T) {
	client := &mockAMQPClient{}
	var calledOrder string
	svc := &mockInitiator{
		initiateFn: func(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error) {
			calledOrder = orderID
			return &service.PaymentOutput{ID: "pay_test"}, nil
		},
	}
	c := NewOrderCreatedConsumer(client, svc, nil)

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
