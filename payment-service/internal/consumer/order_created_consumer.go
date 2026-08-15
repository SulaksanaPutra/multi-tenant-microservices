package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	"payment-service/internal/domain"
	"payment-service/internal/service"
)

type AMQPClient interface {
	ConnContext() context.Context
	WaitUntilReady(ctx context.Context) error
	DeclareExchange(name, kind string) error
	GetChannel() *amqp.Channel
}

type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

type InboxService interface {
	ClaimEvent(txCtx context.Context, input service.ClaimInboxInput) (bool, error)
}

type PaymentInitiator interface {
	InitiatePayment(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error)
}

type OrderCreatedConsumerParams struct {
	Client         AMQPClient
	TxManager      TxManager
	InboxService   InboxService
	PaymentService PaymentInitiator
	Logger         *slog.Logger
}

type OrderCreatedConsumer struct {
	client         AMQPClient
	txManager      TxManager
	inboxService   InboxService
	paymentService PaymentInitiator
	logger         *slog.Logger
}

func NewOrderCreatedConsumer(params OrderCreatedConsumerParams) *OrderCreatedConsumer {
	logger := params.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &OrderCreatedConsumer{
		client:         params.Client,
		txManager:      params.TxManager,
		inboxService:   params.InboxService,
		paymentService: params.PaymentService,
		logger:         logger,
	}
}

func (c *OrderCreatedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := c.client.ConnContext()
			if err := c.runConsumerLoop(ctx, connCtx); err != nil {
				c.logger.Warn("consumer loop exited with error", "err", err)
			}

			if ctx.Err() != nil {
				return
			}

			c.logger.Info("waiting for RabbitMQ reconnection...")
			if err := c.client.WaitUntilReady(ctx); err != nil {
				return
			}
			c.logger.Info("reconnected to RabbitMQ; restarting OrderCreatedConsumer...")
		}
	}()
	return nil
}

func (c *OrderCreatedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	ch := c.client.GetChannel()
	if ch == nil {
		return errors.New("rabbitmq channel is nil")
	}

	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	_, err := ch.QueueDeclare(
		domain.QueuePaymentServiceOrderCreated,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to declare queue %s: %w", domain.QueuePaymentServiceOrderCreated, err)
	}

	err = ch.QueueBind(
		domain.QueuePaymentServiceOrderCreated,
		domain.RoutingKeyOrderCreated,
		domain.ExchangeCompanyEvents,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to bind queue to exchange: %w", err)
	}

	deliveries, err := ch.Consume(
		domain.QueuePaymentServiceOrderCreated,
		"payment-service-order-created",
		false, // autoAck
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to consume from %s: %w", domain.QueuePaymentServiceOrderCreated, err)
	}

	c.logger.Info("started OrderCreatedConsumer listening on queue", "queue", domain.QueuePaymentServiceOrderCreated)

	for {
		select {
		case <-appCtx.Done():
			c.logger.Info("stopping OrderCreatedConsumer (application context done)")
			return appCtx.Err()
		case <-connCtx.Done():
			c.logger.Warn("stopping OrderCreatedConsumer (connection context closed)")
			return connCtx.Err()
		case d, ok := <-deliveries:
			if !ok {
				c.logger.Warn("delivery channel closed for OrderCreatedConsumer")
				return errors.New("delivery channel closed")
			}
			c.handleDelivery(appCtx, d)
		}
	}
}

func (c *OrderCreatedConsumer) handleDelivery(ctx context.Context, d amqp.Delivery) {
	if d.RoutingKey != domain.RoutingKeyOrderCreated && d.RoutingKey != "" {
		c.logger.Warn("received misrouted message; discarding", "routing_key", d.RoutingKey, "expected", domain.RoutingKeyOrderCreated)
		_ = d.Ack(false)
		return
	}

	var evt domain.OrderCreatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		c.logger.Error("failed to unmarshal OrderCreatedEvent payload", "err", err)
		_ = d.Nack(false, false)
		return
	}

	deliveryCount := getDeliveryCount(d.Headers)
	if deliveryCount >= 3 {
		c.logger.Warn("[DLQ] max delivery count reached for event; discarding to DLQ",
			"event_id", evt.EventID,
			"order_id", evt.OrderID,
			"tenant_id", evt.TenantID,
			"delivery_count", deliveryCount,
		)
		_ = d.Nack(false, false)
		return
	}

	c.logger.Info("received order.created event", "event_id", evt.EventID, "order_id", evt.OrderID, "tenant_id", evt.TenantID)

	err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		isDup, err := c.inboxService.ClaimEvent(txCtx, service.ClaimInboxInput{
			EventID:   evt.EventID,
			TenantID:  evt.TenantID,
			EventType: domain.RoutingKeyOrderCreated,
			Payload:   d.Body,
		})
		if err != nil {
			return fmt.Errorf("inbox guard failed: %w", err)
		}
		if isDup {
			c.logger.Info("duplicate order.created event detected by inbox guard; skipping", "event_id", evt.EventID)
			return nil
		}

		_, err = c.paymentService.InitiatePayment(txCtx, evt.TenantID, evt.OrderID, evt.Amount, "USD")
		if err != nil {
			return fmt.Errorf("failed to initiate payment: %w", err)
		}
		return nil
	})

	if err != nil {
		c.logger.Error("failed to process order.created event; requeueing", "event_id", evt.EventID, "order_id", evt.OrderID, "err", err)
		_ = d.Nack(false, true)
		return
	}

	_ = d.Ack(false)
	c.logger.Info("successfully processed & ACKed order.created event", "event_id", evt.EventID, "order_id", evt.OrderID)
}
