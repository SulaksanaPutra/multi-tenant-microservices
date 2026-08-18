package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"payment-service/internal/domain"
	"payment-service/internal/infrastructure/rabbitmq"
	"payment-service/internal/service"
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

func (m *mockAcknowledger) Nack(tag uint64, multiple bool, requeue bool) error {
	m.nackCalled = true
	m.multipleVal = multiple
	m.requeueVal = requeue
	return nil
}

func (m *mockAcknowledger) Reject(tag uint64, requeue bool) error {
	m.nackCalled = true
	m.requeueVal = requeue
	return nil
}

type mockAMQPClient struct {
	declareErr error
	bindErr    error
	consumeErr error
}

func (m *mockAMQPClient) ConnContext() context.Context {
	return context.Background()
}

func (m *mockAMQPClient) WaitUntilReady(ctx context.Context) error {
	return nil
}

func (m *mockAMQPClient) DeclareExchange(name, kind string) error {
	return m.declareErr
}

func (m *mockAMQPClient) DeclareAndBindQueue(queueName, exchangeName, routingKey string) error {
	return m.bindErr
}

func (m *mockAMQPClient) Consume(queueName, consumerTag string) (<-chan rabbitmq.Delivery, error) {
	ch := make(chan rabbitmq.Delivery)
	close(ch)
	return ch, m.consumeErr
}

type mockTxManager struct {
	withTxFn func(ctx context.Context, fn func(txCtx context.Context) error) error
}

func (m *mockTxManager) WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	if m.withTxFn != nil {
		return m.withTxFn(ctx, fn)
	}
	return fn(ctx)
}

type mockInboxService struct {
	claimEventFn func(txCtx context.Context, input service.ClaimInboxInput) (bool, error)
}

func (m *mockInboxService) ClaimEvent(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
	if m.claimEventFn != nil {
		return m.claimEventFn(txCtx, input)
	}
	return false, nil
}

type mockInitiator struct {
	initiateFn             func(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error)
	generateInstructionsFn func(ctx context.Context, paymentID string) error
}

func (m *mockInitiator) InitiatePayment(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error) {
	if m.initiateFn != nil {
		return m.initiateFn(ctx, tenantID, orderID, amount, currency)
	}
	return &service.PaymentOutput{ID: "pay_1"}, nil
}

func (m *mockInitiator) GeneratePaymentInstructions(ctx context.Context, paymentID string) error {
	if m.generateInstructionsFn != nil {
		return m.generateInstructionsFn(ctx, paymentID)
	}
	return nil
}

func TestOrderCreatedConsumer_HandleDelivery(t *testing.T) {
	validEvt := domain.OrderCreatedEvent{
		EventID:  "evt_1",
		TenantID: "tnt_1",
		OrderID:  "ord_99",
		Amount:   50.0,
	}
	validBody, _ := json.Marshal(validEvt)

	t.Run("success claims inbox and initiates payment", func(t *testing.T) {
		mockAck := &mockAcknowledger{}
		var claimedInput service.ClaimInboxInput
		inboxService := &mockInboxService{
			claimEventFn: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
				claimedInput = input
				return false, nil
			},
		}
		var initiatedOrder string
		var generatedPaymentID string
		paymentService := &mockInitiator{
			initiateFn: func(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error) {
				initiatedOrder = orderID
				return &service.PaymentOutput{ID: "pay_99"}, nil
			},
			generateInstructionsFn: func(ctx context.Context, paymentID string) error {
				generatedPaymentID = paymentID
				return nil
			},
		}

		orderCreatedConsumer := NewOrderCreatedConsumer(OrderCreatedConsumerParams{
			Client:         &mockAMQPClient{},
			TxManager:      &mockTxManager{},
			InboxService:   inboxService,
			PaymentService: paymentService,
		})

		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			RoutingKey:   domain.RoutingKeyOrderCreated,
			Body:         validBody,
		}

		orderCreatedConsumer.handleDelivery(context.Background(), d)

		if !mockAck.ackCalled {
			t.Error("expected message to be ACKed")
		}
		if claimedInput.EventID != "evt_1" {
			t.Errorf("expected EventID 'evt_1', got '%s'", claimedInput.EventID)
		}
		if initiatedOrder != "ord_99" {
			t.Errorf("expected order 'ord_99' to be initiated, got '%s'", initiatedOrder)
		}
		if generatedPaymentID != "pay_99" {
			t.Errorf("expected payment 'pay_99' instructions to be generated, got '%s'", generatedPaymentID)
		}
	})

	t.Run("duplicate inbox event skips payment initiation and acks", func(t *testing.T) {
		mockAck := &mockAcknowledger{}
		inboxService := &mockInboxService{
			claimEventFn: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
				return true, nil // duplicate
			},
		}
		initiated := false
		paymentService := &mockInitiator{
			initiateFn: func(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error) {
				initiated = true
				return nil, nil
			},
		}

		orderCreatedConsumer := NewOrderCreatedConsumer(OrderCreatedConsumerParams{
			Client:         &mockAMQPClient{},
			TxManager:      &mockTxManager{},
			InboxService:   inboxService,
			PaymentService: paymentService,
		})

		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			RoutingKey:   domain.RoutingKeyOrderCreated,
			Body:         validBody,
		}

		orderCreatedConsumer.handleDelivery(context.Background(), d)

		if !mockAck.ackCalled {
			t.Error("expected duplicate event to be ACKed")
		}
		if initiated {
			t.Error("expected payment initiation to be skipped for duplicate")
		}
	})

	t.Run("invalid json nacks without requeue", func(t *testing.T) {
		mockAck := &mockAcknowledger{}
		orderCreatedConsumer := NewOrderCreatedConsumer(OrderCreatedConsumerParams{
			Client:         &mockAMQPClient{},
			TxManager:      &mockTxManager{},
			InboxService:   &mockInboxService{},
			PaymentService: &mockInitiator{},
		})

		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			RoutingKey:   domain.RoutingKeyOrderCreated,
			Body:         []byte("invalid json"),
		}

		orderCreatedConsumer.handleDelivery(context.Background(), d)

		if !mockAck.nackCalled {
			t.Error("expected invalid json to be NACKed")
		}
		if mockAck.requeueVal {
			t.Error("expected requeue=false for bad json")
		}
	})

	t.Run("max delivery count nacks without requeue for DLQ", func(t *testing.T) {
		mockAck := &mockAcknowledger{}
		orderCreatedConsumer := NewOrderCreatedConsumer(OrderCreatedConsumerParams{
			Client:         &mockAMQPClient{},
			TxManager:      &mockTxManager{},
			InboxService:   &mockInboxService{},
			PaymentService: &mockInitiator{},
		})

		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			RoutingKey:   domain.RoutingKeyOrderCreated,
			Body:         validBody,
			Headers: map[string]interface{}{
				"x-delivery-count": 3,
			},
		}

		orderCreatedConsumer.handleDelivery(context.Background(), d)

		if !mockAck.nackCalled {
			t.Error("expected poison pill to be NACKed")
		}
		if mockAck.requeueVal {
			t.Error("expected requeue=false for max delivery count")
		}
	})

	t.Run("misrouted key acks and discards", func(t *testing.T) {
		mockAck := &mockAcknowledger{}
		orderCreatedConsumer := NewOrderCreatedConsumer(OrderCreatedConsumerParams{
			Client:         &mockAMQPClient{},
			TxManager:      &mockTxManager{},
			InboxService:   &mockInboxService{},
			PaymentService: &mockInitiator{},
		})

		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			RoutingKey:   "user.created", // misrouted
			Body:         validBody,
		}

		orderCreatedConsumer.handleDelivery(context.Background(), d)

		if !mockAck.ackCalled {
			t.Error("expected misrouted message to be ACKed to discard")
		}
	})

	t.Run("payment initiation failure nacks with requeue", func(t *testing.T) {
		mockAck := &mockAcknowledger{}
		paymentService := &mockInitiator{
			initiateFn: func(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error) {
				return nil, errors.New("psp timeout")
			},
		}

		orderCreatedConsumer := NewOrderCreatedConsumer(OrderCreatedConsumerParams{
			Client:         &mockAMQPClient{},
			TxManager:      &mockTxManager{},
			InboxService:   &mockInboxService{},
			PaymentService: paymentService,
		})

		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			RoutingKey:   domain.RoutingKeyOrderCreated,
			Body:         validBody,
		}

		orderCreatedConsumer.handleDelivery(context.Background(), d)

		if !mockAck.nackCalled {
			t.Error("expected failure to be NACKed")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true on transient error")
		}
	})
}
