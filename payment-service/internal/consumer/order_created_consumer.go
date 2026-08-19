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
	Client                 AMQPClient
	TxManager              TxManager
	InboxService           InboxService
	PaymentService         PaymentService
	PaymentProviderService PaymentProviderService
	Logger                 *slog.Logger
}

type OrderCreatedConsumer struct {
	client                 AMQPClient
	txManager              TxManager
	inboxService           InboxService
	paymentService         PaymentService
	paymentProviderService PaymentProviderService
	logger                 *slog.Logger
}

func NewOrderCreatedConsumer(params OrderCreatedConsumerParams) *OrderCreatedConsumer {
	logger := params.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &OrderCreatedConsumer{
		client:                 params.Client,
		txManager:              params.TxManager,
		inboxService:           params.InboxService,
		paymentService:         params.PaymentService,
		paymentProviderService: params.PaymentProviderService,
		logger:                 logger,
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

	var paymentOutput *service.PaymentOutput
	var isDup bool

	// STEP 1: DB Transaction (<5ms) - Inbox Claim & Initial PENDING Payment Write
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

		var initErr error
		paymentOutput, initErr = orderCreatedConsumer.paymentService.InitiatePayment(txCtx, service.InitiatePaymentInput{
			TenantID: evt.TenantID,
			OrderID:  evt.OrderID,
			Amount:   evt.Amount,
			Currency: "USD",
		})
		if initErr != nil {
			return fmt.Errorf("failed to initiate payment: %w", initErr)
		}
		return nil
	})

	if err != nil {
		orderCreatedConsumer.logger.Error("failed to process order.created event; requeueing", "event_id", evt.EventID, "order_id", evt.OrderID, "err", err)
		_ = d.Nack(false, true)
		return
	}

	if isDup {
		_ = d.Ack(false)
		return
	}

	if paymentOutput == nil {
		_ = d.Ack(false)
		return
	}

	// STEP 2: Pure External Gateway I/O (OUTSIDE DB Transaction - Rule 5.4 Compliant)
	execOut, execErr := orderCreatedConsumer.paymentProviderService.ExecuteFallback(ctx, service.ExecuteFallbackInput{
		TenantID:    evt.TenantID,
		PaymentID:   paymentOutput.ID,
		OrderID:     evt.OrderID,
		Amount:      evt.Amount,
		Currency:    "USD",
		Description: fmt.Sprintf("Order %s", evt.OrderID),
		ReturnURL:   fmt.Sprintf("http://localhost:8000/orders/%s", evt.OrderID),
	})

	// STEP 3: DB Transaction (<5ms) - Persist Generated Instructions & Outbox Event
	if execErr != nil || execOut == nil || execOut.Session == nil {
		failedAttempts := []domain.ProviderType{}
		attemptErrors := map[domain.ProviderType]error{}
		errMsg := "no available payment provider"
		if execErr != nil {
			errMsg = execErr.Error()
		}
		if execOut != nil {
			failedAttempts = execOut.FailedAttempts
			attemptErrors = execOut.AttemptErrors
		}

		orderCreatedConsumer.logger.Error("fallback chain failed to generate payment instructions",
			"payment_id", paymentOutput.ID, "err", errMsg)

		_ = orderCreatedConsumer.paymentService.FailInstructionGeneration(ctx, service.FailInstructionInput{
			PaymentID:      paymentOutput.ID,
			Reason:         errMsg,
			FailedAttempts: failedAttempts,
			AttemptErrors:  attemptErrors,
		})
	} else {
		if err := orderCreatedConsumer.paymentService.CompleteInstructionGeneration(ctx, service.CompleteInstructionInput{
			PaymentID:         paymentOutput.ID,
			Provider:          execOut.Provider,
			ExternalSessionID: execOut.Session.ExternalSessionID,
			Instructions:      execOut.Session.Instructions,
			FailedAttempts:    execOut.FailedAttempts,
			AttemptErrors:     execOut.AttemptErrors,
		}); err != nil {
			orderCreatedConsumer.logger.Error("failed to persist payment instructions", "payment_id", paymentOutput.ID, "err", err)
		}
	}

	_ = d.Ack(false)
	orderCreatedConsumer.logger.Info("successfully processed & ACKed order.created event", "event_id", evt.EventID, "order_id", evt.OrderID)
}
