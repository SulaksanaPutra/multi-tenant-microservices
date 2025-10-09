package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
	"notification-service/internal/txctx"
)

const (
	RoutingKeyUserCreated           = "user.created"
	QueueNotificationUserCreated    = "notification_service_user_created"
)

type UserCreatedEvent struct {
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	TenantID  string    `json:"tenant_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type UserCreatedConsumer struct {
	txManager           txctx.TxManager
	client              *rabbitmq.Client
	notificationService service.NotificationService
}

func NewUserCreatedConsumer(txManager txctx.TxManager, client *rabbitmq.Client, notifSvc service.NotificationService) (*UserCreatedConsumer, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := client.DeclareAndBindQueue(QueueNotificationUserCreated, ExchangeCompanyEvents, RoutingKeyUserCreated); err != nil {
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	return &UserCreatedConsumer{
		txManager:           txManager,
		client:              client,
		notificationService: notifSvc,
	}, nil
}

func (c *UserCreatedConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		QueueNotificationUserCreated,  // queue
		"notification-user-worker",    // consumer tag
		false,                          // auto-ack
		false,                          // exclusive
		false,                          // no-local
		false,                          // no-wait
		nil,                            // args
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue %s: %w", QueueNotificationUserCreated, err)
	}

	log.Printf("Notification Service worker listening for events on queue '%s'...", QueueNotificationUserCreated)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("UserCreatedConsumer: Context cancelled, shutting down.")
				return
			case d, ok := <-msgs:
				if !ok {
					log.Printf("UserCreatedConsumer: Message channel closed.")
					return
				}
				log.Printf("Received UserCreated message from queue '%s'", QueueNotificationUserCreated)

				var evt UserCreatedEvent
				if err := json.Unmarshal(d.Body, &evt); err != nil {
					log.Printf("Error unmarshaling UserCreated payload: %v", err)
					_ = d.Nack(false, false)
					continue
				}

				input := service.ProcessEventInput{
					EventID:    evt.EventID,
					UserID:     evt.UserID,
					TenantID:   evt.TenantID,
					EventType:  "user.created",
					OwnerEmail: evt.Email,
					Payload:    d.Body,
				}

				err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
					return c.notificationService.ProcessEventAndTrySendWelcome(txCtx, input)
				})

				if err != nil {
					log.Printf("Error processing user created notification: %v", err)
					_ = d.Nack(false, true)
					continue
				}

				_ = d.Ack(false)
			}
		}
	}()

	return nil
}
