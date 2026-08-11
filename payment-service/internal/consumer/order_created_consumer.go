package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	"payment-service/internal/domain"
	"payment-service/internal/service"
)

type AMQPChannel interface {
	QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error)
	QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error
	Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error)
}

type PaymentInitiator interface {
	InitiatePayment(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*service.PaymentOutput, error)
}

type OrderCreatedConsumer struct {
	channel        AMQPChannel
	paymentService PaymentInitiator
	logger         *slog.Logger
}

func NewOrderCreatedConsumer(channel AMQPChannel, paymentService PaymentInitiator, logger *slog.Logger) *OrderCreatedConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &OrderCreatedConsumer{
		channel:        channel,
		paymentService: paymentService,
		logger:         logger,
	}
}

func (c *OrderCreatedConsumer) SetupTopology() error {
	_, err := c.channel.QueueDeclare(
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

	err = c.channel.QueueBind(
		domain.QueuePaymentServiceOrderCreated,
		domain.RoutingKeyOrderCreated,
		domain.ExchangeCompanyEvents,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to bind queue to exchange: %w", err)
	}

	return nil
}

func (c *OrderCreatedConsumer) Start(ctx context.Context) error {
	if err := c.SetupTopology(); err != nil {
		return err
	}

	deliveries, err := c.channel.Consume(
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

	go func() {
		for {
			select {
			case <-ctx.Done():
				c.logger.Info("stopping OrderCreatedConsumer")
				return
			case d, ok := <-deliveries:
				if !ok {
					c.logger.Warn("delivery channel closed for OrderCreatedConsumer")
					return
				}
				c.handleDelivery(ctx, d)
			}
		}
	}()

	return nil
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
