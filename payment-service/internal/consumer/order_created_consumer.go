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

type PaymentInitiator interface {
	InitiatePayment(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error)
}

type OrderCreatedConsumer struct {
	client         AMQPClient
	paymentService PaymentInitiator
	logger         *slog.Logger
}

func NewOrderCreatedConsumer(client AMQPClient, paymentService PaymentInitiator, logger *slog.Logger) *OrderCreatedConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &OrderCreatedConsumer{
		client:         client,
		paymentService: paymentService,
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
	var evt domain.OrderCreatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		c.logger.Error("failed to unmarshal OrderCreatedEvent payload", "err", err)
		_ = d.Nack(false, false)
		return
	}

	c.logger.Info("received order.created event", "event_id", evt.EventID, "order_id", evt.OrderID, "tenant_id", evt.TenantID)

	_, err := c.paymentService.InitiatePayment(ctx, evt.TenantID, evt.OrderID, evt.Amount, "USD")
	if err != nil {
		c.logger.Error("failed to initiate payment for order", "order_id", evt.OrderID, "err", err)
		_ = d.Nack(false, true)
		return
	}

	_ = d.Ack(false)
}
