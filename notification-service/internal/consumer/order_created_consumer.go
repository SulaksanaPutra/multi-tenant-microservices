package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"notification-service/internal/domain"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
)

type OrderCreatedConsumerParams struct {
	TxManager           TxManager
	Client              AMQPClient
	InboxService        InboxService
	NotificationService OrderNotificationService
}

type OrderCreatedConsumer struct {
	txManager           TxManager
	client              AMQPClient
	inboxService        InboxService
	notificationService OrderNotificationService
}

func NewOrderCreatedConsumer(params OrderCreatedConsumerParams) *OrderCreatedConsumer {
	return &OrderCreatedConsumer{
		txManager:           params.TxManager,
		client:              params.Client,
		inboxService:        params.InboxService,
		notificationService: params.NotificationService,
	}
}

func (orderCreatedConsumer *OrderCreatedConsumer) setupTopology() error {
	if err := orderCreatedConsumer.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := orderCreatedConsumer.client.DeclareAndBindQueue(domain.QueueNotificationOrderCreated, domain.ExchangeCompanyEvents, domain.RoutingKeyOrderCreated); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	return nil
}

func (orderCreatedConsumer *OrderCreatedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := orderCreatedConsumer.client.ConnContext()

			err := orderCreatedConsumer.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("OrderCreatedConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := orderCreatedConsumer.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("OrderCreatedConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (orderCreatedConsumer *OrderCreatedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := orderCreatedConsumer.setupTopology(); err != nil {
		return err
	}

	msgs, err := orderCreatedConsumer.client.Consume(
		domain.QueueNotificationOrderCreated,
		"notification-order-created-consumer",
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("NotificationService listening for '%s' events on queue '%s'...", domain.RoutingKeyOrderCreated, domain.QueueNotificationOrderCreated)

	for {
		select {
		case <-appCtx.Done():
			log.Printf("OrderCreatedConsumer: Context cancelled, shutting down.")
			return appCtx.Err()

		case <-connCtx.Done():
			return connCtx.Err()

		case d, ok := <-msgs:
			if !ok {
				return errors.New("delivery channel closed")
			}

			_ = orderCreatedConsumer.handleDelivery(appCtx, d)
		}
	}
}

func (orderCreatedConsumer *OrderCreatedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	if d.RoutingKey != domain.RoutingKeyOrderCreated && d.RoutingKey != "" {
		log.Printf("[WARN] OrderCreatedConsumer: Received misrouted message with routing_key='%s' (expected '%s'). Discarding.", d.RoutingKey, domain.RoutingKeyOrderCreated)
		_ = d.Ack(false)
		return nil
	}

	var evt domain.OrderCreatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("Error unmarshaling OrderCreated payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	deliveryCount := getDeliveryCount(d.Headers)
	if deliveryCount >= 3 {
		log.Printf("[DLQ] OrderCreatedConsumer: Max delivery count reached for event_id='%s' order_id='%s' (delivery_count=%d). Routing to DLQ.",
			evt.EventID, evt.OrderID, deliveryCount)
		_ = d.Nack(false, false)
		return errors.New("max delivery count reached")
	}

	log.Printf("OrderCreatedConsumer processing event_id='%s' for order_id='%s' tenant_id='%s'", evt.EventID, evt.OrderID, evt.TenantID)

	err := orderCreatedConsumer.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		inboxInput := service.ClaimInboxInput{
			EventID:   evt.EventID,
			TenantID:  evt.TenantID,
			EventType: domain.RoutingKeyOrderCreated,
			Payload:   d.Body,
		}

		isDup, err := orderCreatedConsumer.inboxService.ClaimEvent(txCtx, inboxInput)
		if err != nil {
			return fmt.Errorf("inbox guard failed: %w", err)
		}
		if isDup {
			log.Printf("OrderCreatedConsumer: Duplicate event_id='%s' detected by Inbox guard. Skipping.", evt.EventID)
			return nil
		}

		log.Printf("OrderCreatedConsumer: Processed order created event_id='%s' order_id='%s' amount=%.2f status='%s'",
			evt.EventID, evt.OrderID, evt.Amount, evt.Status)

		if err := orderCreatedConsumer.notificationService.CreateOrderNotification(txCtx, evt); err != nil {
			return fmt.Errorf("failed to persist order notification: %w", err)
		}
		log.Printf("OrderCreatedConsumer: Recorded notification for order_id='%s' tenant_id='%s'", evt.OrderID, evt.TenantID)

		return nil
	})

	if err != nil {
		log.Printf("OrderCreatedConsumer Error: Failed to handle OrderCreated for event '%s': %v", evt.EventID, err)
		_ = d.Nack(false, true) // Requeue
		return err
	}

	_ = d.Ack(false)
	log.Printf("OrderCreatedConsumer: Successfully processed & ACKed event_id='%s'", evt.EventID)
	return nil
}
