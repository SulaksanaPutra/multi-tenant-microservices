package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"payment-service/internal/domain"
	"payment-service/internal/infrastructure/rabbitmq"
	"payment-service/internal/service"
)

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
				if ctx.Err() != nil {
					return
				}
				c.logger.Error("OrderCreatedConsumer consumer loop stopped", "error", err)
			}
			if ctx.Err() != nil {
				return
			}
			c.logger.Info("waiting for RabbitMQ to become ready...")
			if err := c.client.WaitUntilReady(ctx); err != nil {
				c.logger.Error("context cancelled while waiting for RabbitMQ ready", "error", err)
				return
			}
			c.logger.Info("reconnected to RabbitMQ; restarting OrderCreatedConsumer...")
		}
	}()
	return nil
}

func (c *OrderCreatedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := c.client.DeclareAndBindQueue(
		domain.QueuePaymentServiceOrderCreated,
		domain.ExchangeCompanyEvents,
		domain.RoutingKeyOrderCreated,
	); err != nil {
		return fmt.Errorf("failed to bind queue to exchange: %w", err)
	}

	deliveries, err := c.client.Consume(
		domain.QueuePaymentServiceOrderCreated,
		"payment-service-order-created",
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

func (c *OrderCreatedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) {
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
