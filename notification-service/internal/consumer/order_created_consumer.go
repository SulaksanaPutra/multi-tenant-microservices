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

// OrderNotificationService is the consumer-side interface expected by OrderCreatedConsumer.
type OrderNotificationService interface {
	CreateOrderNotification(ctx context.Context, evt domain.OrderCreatedEvent) error
}

type OrderCreatedConsumerParams struct {
	TxManager           TxManager
	Client              *rabbitmq.Client
	InboxService        InboxService
	NotificationService OrderNotificationService
}

type OrderCreatedConsumer struct {
	txManager           TxManager
	client              *rabbitmq.Client
	inboxService        InboxService
	notificationService OrderNotificationService
}

func NewOrderCreatedConsumer(params OrderCreatedConsumerParams) (*OrderCreatedConsumer, error) {
	if params.InboxService == nil {
		return nil, errors.New("inboxService is required")
	}
	if params.NotificationService == nil {
		return nil, errors.New("notificationService is required")
	}

	consumer := &OrderCreatedConsumer{
		txManager:           params.TxManager,
		client:              params.Client,
		inboxService:        params.InboxService,
		notificationService: params.NotificationService,
	}

	if err := consumer.setupTopology(); err != nil {
		return nil, err
	}

	return consumer, nil
}

func (c *OrderCreatedConsumer) setupTopology() error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := c.client.DeclareAndBindQueue(domain.QueueNotificationOrderCreated, domain.ExchangeCompanyEvents, domain.RoutingKeyOrderCreated); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	return nil
}

func (c *OrderCreatedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := c.client.ConnContext()

			err := c.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("OrderCreatedConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := c.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("OrderCreatedConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (c *OrderCreatedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := c.setupTopology(); err != nil {
		return err
	}

	if c.client == nil || c.client.Channel == nil {
		return errors.New("channel is nil")
	}

	msgs, err := c.client.Channel.Consume(
		domain.QueueNotificationOrderCreated,  // queue
		"notification-order-created-consumer", // consumer tag
		false,                                 // auto-ack
		false,                                 // exclusive
		false,                                 // no-local
		false,                                 // no-wait
		nil,                                   // args
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

			_ = c.handleDelivery(appCtx, d)
		}
	}
}

func (c *OrderCreatedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
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

	log.Printf("OrderCreatedConsumer processing event_id='%s' for order_id='%s' tenant_id='%s'", evt.EventID, evt.OrderID, evt.TenantID)

	err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		inboxInput := service.ClaimInboxInput{
			EventID:   evt.EventID,
			TenantID:  evt.TenantID,
			EventType: domain.RoutingKeyOrderCreated,
			Payload:   d.Body,
		}

		isDup, err := c.inboxService.ClaimEvent(txCtx, inboxInput)
		if err != nil {
			return fmt.Errorf("inbox guard failed: %w", err)
		}
		if isDup {
			log.Printf("OrderCreatedConsumer: Duplicate event_id='%s' detected by Inbox guard. Skipping.", evt.EventID)
			return nil
		}

		log.Printf("OrderCreatedConsumer: Processed order created event_id='%s' order_id='%s' amount=%.2f status='%s'",
			evt.EventID, evt.OrderID, evt.Amount, evt.Status)

		if err := c.notificationService.CreateOrderNotification(txCtx, evt); err != nil {
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
