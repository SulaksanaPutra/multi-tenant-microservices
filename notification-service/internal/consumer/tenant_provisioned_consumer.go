package consumer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
	"notification-service/internal/txctx"
)

const (
	// ExchangeCompanyEvents is a local copy of the exchange name declared in tenant-service/publisher.
	ExchangeCompanyEvents               = "company.events"
	RoutingKeyTenantProvisioned         = "tenant.provisioned"
	QueueNotificationTenantProvisioned = "notification_service_tenant_provisioned"
)

type TenantProvisionedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	TenantSlug string `json:"tenant_slug"`
	UserID     string `json:"user_id"`
}

type TenantProvisionedConsumer struct {
	db                  *sql.DB
	client              *rabbitmq.Client
	notificationService service.NotificationService
}

func NewTenantProvisionedConsumer(db *sql.DB, client *rabbitmq.Client, notifSvc service.NotificationService) (*TenantProvisionedConsumer, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := client.DeclareAndBindQueue(QueueNotificationTenantProvisioned, ExchangeCompanyEvents, RoutingKeyTenantProvisioned); err != nil {
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	return &TenantProvisionedConsumer{
		db:                  db,
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
		for {
			select {
			case <-ctx.Done():
				log.Printf("TenantProvisionedConsumer: Context cancelled, shutting down.")
				return
			case d, ok := <-msgs:
				if !ok {
					log.Printf("TenantProvisionedConsumer: Message channel closed.")
					return
				}
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

				// 1. Manage Transaction Boundary at Consumer Layer (for Inbox guard & notification log)
				tx, err := c.db.BeginTx(ctx, nil)
				if err != nil {
					log.Printf("Error starting transaction in consumer: %v", err)
					d.Nack(false, true)
					continue
				}

				msgCtx := txctx.WithTx(ctx, tx)
				input := service.SendWelcomeNotificationInput{
					EventID:  evt.EventID,
					UserID:   evt.UserID,
					TenantID: evt.TenantID,
				}

				// 2. Delegate to NotificationService business layer
				if err := c.notificationService.SendWelcomeNotification(msgCtx, input); err != nil {
					tx.Rollback()
					log.Printf("Error processing notification: %v", err)
					d.Nack(false, true)
					continue
				}

				// 3. Commit Transaction (Inbox INSERT + Notification log)
				if err := tx.Commit(); err != nil {
					tx.Rollback()
					log.Printf("Error committing notification transaction: %v", err)
					d.Nack(false, true)
					continue
				}

				d.Ack(false)
			}
		}
	}()

	return nil
}
