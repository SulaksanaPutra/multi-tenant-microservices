package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
)

const (
	ExchangeCompanyEvents               = "company.events"
	RoutingKeyTenantProvisioned         = "tenant.provisioned"
	QueueNotificationTenantProvisioned = "notification_service_tenant_provisioned"
)

// TenantProvisionedEvent mirrors the payload published by tenant-service.
// EventID carries the originating outbox row ID — this is the Inbox Pattern
// deduplication key used by the notification-service to prevent duplicate emails.
type TenantProvisionedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	TenantSlug string `json:"tenant_slug"`
	UserID     string `json:"user_id"`
}

type TenantProvisionedConsumer struct {
	client              *rabbitmq.Client
	notificationService service.NotificationService
}

func NewTenantProvisionedConsumer(client *rabbitmq.Client, notifSvc service.NotificationService) (*TenantProvisionedConsumer, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := client.DeclareAndBindQueue(QueueNotificationTenantProvisioned, ExchangeCompanyEvents, RoutingKeyTenantProvisioned); err != nil {
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	return &TenantProvisionedConsumer{
		client:              client,
		notificationService: notifSvc,
	}, nil
}

func (c *TenantProvisionedConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		QueueNotificationTenantProvisioned, // queue
		"notification-service-worker",       // consumer tag
		false,                               // auto-ack
		false,                               // exclusive
		false,                               // no-local
		false,                               // no-wait
		nil,                                 // args
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue %s: %w", QueueNotificationTenantProvisioned, err)
	}

	log.Printf("Notification Service worker listening for events on queue '%s'...", QueueNotificationTenantProvisioned)

	go func() {
		for d := range msgs {
			log.Printf("Received TenantProvisioned message from queue '%s'", QueueNotificationTenantProvisioned)

			var evt TenantProvisionedEvent
			if err := json.Unmarshal(d.Body, &evt); err != nil {
				log.Printf("Error unmarshaling TenantProvisioned payload: %v", err)
				d.Nack(false, false)
				continue
			}

			if evt.EventID == "" {
				log.Printf("Warning: TenantProvisioned message missing event_id for tenant_id='%s'. Processing without Inbox guard.", evt.TenantID)
			}

			log.Printf("Processing notification for event_id='%s', tenant_id='%s', user_id='%s'",
				evt.EventID, evt.TenantID, evt.UserID)

			input := service.SendWelcomeNotificationInput{
				EventID:  evt.EventID,
				UserID:   evt.UserID,
				TenantID: evt.TenantID,
			}

			// Delegate to NotificationService business layer (Inbox guard runs inside)
			if err := c.notificationService.SendWelcomeNotification(ctx, input); err != nil {
				log.Printf("Error processing notification: %v", err)
				d.Nack(false, true)
				continue
			}

			d.Ack(false)
		}
	}()

	return nil
}
