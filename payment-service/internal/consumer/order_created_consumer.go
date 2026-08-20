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
	Client       AMQPClient
	TxManager    TxManager
	InboxService InboxService
	DebtService  DebtService
	Logger       *slog.Logger
}

type OrderCreatedConsumer struct {
	client       AMQPClient
	txManager    TxManager
	inboxService InboxService
	debtService  DebtService
	logger       *slog.Logger
}

func NewOrderCreatedConsumer(params OrderCreatedConsumerParams) *OrderCreatedConsumer {
	logger := params.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &OrderCreatedConsumer{
		client:       params.Client,
		txManager:    params.TxManager,
		inboxService: params.InboxService,
		debtService:  params.DebtService,
		logger:       logger,
	}
}

func (orderCreatedConsumer *OrderCreatedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := orderCreatedConsumer.client.ConnContext()
			if err := orderCreatedConsumer.runConsumerLoop(ctx, connCtx); err != nil {
				if ctx.Err() != nil {
					return
				}
				orderCreatedConsumer.logger.Error("OrderCreatedConsumer consumer loop stopped", "error", err)
			}
			if ctx.Err() != nil {
				return
			}
			orderCreatedConsumer.logger.Info("waiting for RabbitMQ to become ready...")
			if err := orderCreatedConsumer.client.WaitUntilReady(ctx); err != nil {
				orderCreatedConsumer.logger.Error("context cancelled while waiting for RabbitMQ ready", "error", err)
				return
			}
			orderCreatedConsumer.logger.Info("reconnected to RabbitMQ; restarting OrderCreatedConsumer...")
		}
	}()
	return nil
}

func (orderCreatedConsumer *OrderCreatedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := orderCreatedConsumer.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := orderCreatedConsumer.client.DeclareAndBindQueue(
		domain.QueuePaymentServiceOrderCreated,
		domain.ExchangeCompanyEvents,
		domain.RoutingKeyOrderCreated,
	); err != nil {
		return fmt.Errorf("failed to bind queue to exchange: %w", err)
	}

	deliveries, err := orderCreatedConsumer.client.Consume(
		domain.QueuePaymentServiceOrderCreated,
		"payment-service-order-created",
	)
	if err != nil {
		return fmt.Errorf("failed to consume from %s: %w", domain.QueuePaymentServiceOrderCreated, err)
	}

	orderCreatedConsumer.logger.Info("started OrderCreatedConsumer listening on queue", "queue", domain.QueuePaymentServiceOrderCreated)

	for {
		select {
		case <-appCtx.Done():
			orderCreatedConsumer.logger.Info("stopping OrderCreatedConsumer (application context done)")
			return appCtx.Err()
		case <-connCtx.Done():
			orderCreatedConsumer.logger.Warn("stopping OrderCreatedConsumer (connection context closed)")
			return connCtx.Err()
		case d, ok := <-deliveries:
			if !ok {
				orderCreatedConsumer.logger.Warn("delivery channel closed for OrderCreatedConsumer")
				return errors.New("delivery channel closed")
			}
			orderCreatedConsumer.handleDelivery(appCtx, d)
		}
	}
}

func (orderCreatedConsumer *OrderCreatedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) {
	if d.RoutingKey != domain.RoutingKeyOrderCreated && d.RoutingKey != "" {
		orderCreatedConsumer.logger.Warn("received misrouted message; discarding", "routing_key", d.RoutingKey, "expected", domain.RoutingKeyOrderCreated)
		_ = d.Ack(false)
		return
	}

	var evt domain.OrderCreatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		orderCreatedConsumer.logger.Error("failed to unmarshal OrderCreatedEvent payload", "err", err)
		_ = d.Nack(false, false)
		return
	}

	deliveryCount := getDeliveryCount(d.Headers)
	if deliveryCount >= 3 {
		orderCreatedConsumer.logger.Warn("[DLQ] max delivery count reached for event; discarding to DLQ",
			"event_id", evt.EventID,
			"order_id", evt.OrderID,
			"tenant_id", evt.TenantID,
			"delivery_count", deliveryCount,
		)
		_ = d.Nack(false, false)
		return
	}

	orderCreatedConsumer.logger.Info("received order.created event", "event_id", evt.EventID, "order_id", evt.OrderID, "tenant_id", evt.TenantID)

	var isDup bool
	err := orderCreatedConsumer.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		var claimErr error
		isDup, claimErr = orderCreatedConsumer.inboxService.ClaimEvent(txCtx, service.ClaimInboxInput{
			EventID:   evt.EventID,
			TenantID:  evt.TenantID,
			EventType: domain.RoutingKeyOrderCreated,
			Payload:   d.Body,
		})
		if claimErr != nil {
			return fmt.Errorf("inbox guard failed: %w", claimErr)
		}
		if isDup {
			orderCreatedConsumer.logger.Info("duplicate order.created event detected by inbox guard; skipping", "event_id", evt.EventID)
			return nil
		}

		_, createErr := orderCreatedConsumer.debtService.CreatePayableDebt(txCtx, service.CreatePayableDebtInput{
			TenantID: evt.TenantID,
			OrderID:  evt.OrderID,
			Amount:   evt.Amount,
			Currency: evt.Currency,
		})
		if createErr != nil {
			return fmt.Errorf("failed to create payable debt: %w", createErr)
		}
		return nil
	})

	if err != nil {
		orderCreatedConsumer.logger.Error("failed to process order.created event; requeueing", "event_id", evt.EventID, "order_id", evt.OrderID, "err", err)
		_ = d.Nack(false, true)
		return
	}

	_ = d.Ack(false)
	orderCreatedConsumer.logger.Info("successfully processed & ACKed order.created event", "event_id", evt.EventID, "order_id", evt.OrderID)
}
