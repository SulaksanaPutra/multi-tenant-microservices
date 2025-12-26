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

// TxManager is the consumer-side interface expected by UserCreatedConsumer.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

type NotificationService interface {
	ProcessEventAndTrySendWelcome(ctx context.Context, input service.ProcessEventInput) error
}

type UserCreatedConsumerParams struct {
	TxManager           TxManager
	Client              *rabbitmq.Client
	NotificationService NotificationService
}

type UserCreatedConsumer struct {
	txManager           TxManager
	client              *rabbitmq.Client
	notificationService NotificationService
}

func NewUserCreatedConsumer(params UserCreatedConsumerParams) (*UserCreatedConsumer, error) {
	consumer := &UserCreatedConsumer{
		txManager:           params.TxManager,
		client:              params.Client,
		notificationService: params.NotificationService,
	}

	if err := consumer.setupTopology(); err != nil {
		return nil, err
	}

	return consumer, nil
}

func (c *UserCreatedConsumer) setupTopology() error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := c.client.DeclareAndBindQueue(domain.QueueNotificationUserCreated, domain.ExchangeCompanyEvents, domain.RoutingKeyUserCreated); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	return nil
}

func (c *UserCreatedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := c.client.ConnContext()

			err := c.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("UserCreatedConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := c.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("UserCreatedConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (c *UserCreatedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := c.setupTopology(); err != nil {
		return err
	}

	if c.client == nil || c.client.Channel == nil {
		return errors.New("channel is nil")
	}

	msgs, err := c.client.Channel.Consume(
		domain.QueueNotificationUserCreated, // queue
		"notification-user-created-consumer", // consumer tag
		false,                                // auto-ack
		false,                                // exclusive
		false,                                // no-local
		false,                                // no-wait
		nil,                                  // args
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("NotificationService listening for '%s' events on queue '%s'...", domain.RoutingKeyUserCreated, domain.QueueNotificationUserCreated)

	for {
		select {
		case <-appCtx.Done():
			log.Printf("UserCreatedConsumer: Context cancelled, shutting down.")
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

func (c *UserCreatedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	var evt domain.UserCreatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("Error unmarshaling UserCreated payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	log.Printf("UserCreatedConsumer processing event_id='%s' for user_id='%s' email='%s'", evt.EventID, evt.UserID, evt.Email)

	err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		input := service.ProcessEventInput{
			EventID:    evt.EventID,
			UserID:     evt.UserID,
			TenantID:   evt.TenantID,
			EventType:  domain.RoutingKeyUserCreated,
			OwnerEmail: evt.Email,
			Payload:    d.Body,
		}
		return c.notificationService.ProcessEventAndTrySendWelcome(txCtx, input)
	})

	if err != nil {
		log.Printf("UserCreatedConsumer Error: Failed to handle UserCreated for event '%s': %v", evt.EventID, err)
		_ = d.Nack(false, true) // Requeue
		return err
	}

	_ = d.Ack(false)
	log.Printf("UserCreatedConsumer: Successfully processed & ACKed event_id='%s'", evt.EventID)
	return nil
}
