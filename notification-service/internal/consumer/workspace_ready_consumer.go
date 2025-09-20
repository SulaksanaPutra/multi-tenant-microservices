package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
	"notification-service/internal/txctx"
)

const (
	ExchangeCompanyEvents           = "company.events"
	RoutingKeyWorkspaceReady        = "workspace.ready"
	QueueNotificationWorkspaceReady = "notification_service_workspace_ready"
)

type WorkspaceReadyEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	OwnerEmail string `json:"owner_email"`
}

type WorkspaceReadyConsumer struct {
	txManager           txctx.TxManager
	client              *rabbitmq.Client
	notificationService service.NotificationService
}

func NewWorkspaceReadyConsumer(txManager txctx.TxManager, client *rabbitmq.Client, notifSvc service.NotificationService) (*WorkspaceReadyConsumer, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := client.DeclareAndBindQueue(QueueNotificationWorkspaceReady, ExchangeCompanyEvents, RoutingKeyWorkspaceReady); err != nil {
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	return &WorkspaceReadyConsumer{
		txManager:           txManager,
		client:              client,
		notificationService: notifSvc,
	}, nil
}

func (c *WorkspaceReadyConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		QueueNotificationWorkspaceReady, // queue
		"notification-service-worker",    // consumer tag
		false,                            // auto-ack
		false,                            // exclusive
		false,                            // no-local
		false,                            // no-wait
		nil,                              // args
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue %s: %w", QueueNotificationWorkspaceReady, err)
	}

	log.Printf("Notification Service worker listening for events on queue '%s'...", QueueNotificationWorkspaceReady)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("WorkspaceReadyConsumer: Context cancelled, shutting down.")
				return
			case d, ok := <-msgs:
				if !ok {
					log.Printf("WorkspaceReadyConsumer: Message channel closed.")
					return
				}
				log.Printf("Received WorkspaceReady message from queue '%s'", QueueNotificationWorkspaceReady)

				var evt WorkspaceReadyEvent
				if err := json.Unmarshal(d.Body, &evt); err != nil {
					log.Printf("Error unmarshaling WorkspaceReady payload: %v", err)
					d.Nack(false, false)
					continue
				}

				input := service.SendWelcomeNotificationInput{
					EventID:    evt.EventID,
					TenantID:   evt.TenantID,
					OwnerEmail: evt.OwnerEmail,
				}

				err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
					return c.notificationService.SendWelcomeNotification(txCtx, input)
				})

				if err != nil {
					log.Printf("Error processing notification: %v", err)
					d.Nack(false, true)
					continue
				}

				d.Ack(false)
			}
		}
	}()

	return nil
}
