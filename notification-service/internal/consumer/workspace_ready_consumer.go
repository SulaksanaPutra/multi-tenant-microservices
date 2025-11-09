package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"notification-service/internal/domain"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
)

type WorkspaceReadyConsumer struct {
	txManager           TxManager
	client              *rabbitmq.Client
	notificationService NotificationService
}

func NewWorkspaceReadyConsumer(txManager TxManager, client *rabbitmq.Client, notifSvc NotificationService) (*WorkspaceReadyConsumer, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := client.DeclareAndBindQueue(domain.QueueNotificationWorkspaceReady, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceReady); err != nil {
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
		domain.QueueNotificationWorkspaceReady, // queue
		"notification-workspace-ready-consumer", // consumer tag
		false,                                   // auto-ack
		false,                                   // exclusive
		false,                                   // no-local
		false,                                   // no-wait
		nil,                                     // args
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue %s: %w", domain.QueueNotificationWorkspaceReady, err)
	}

	log.Printf("NotificationService listening for '%s' events on queue '%s'...", domain.RoutingKeyWorkspaceReady, domain.QueueNotificationWorkspaceReady)

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

				var evt domain.WorkspaceReadyEvent
				if err := json.Unmarshal(d.Body, &evt); err != nil {
					log.Printf("Error unmarshaling WorkspaceReady payload: %v", err)
					_ = d.Nack(false, false)
					continue
				}

				log.Printf("WorkspaceReadyConsumer processing event_id='%s' for tenant_id='%s'", evt.EventID, evt.TenantID)

				err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
					input := service.ProcessEventInput{
						EventID:    evt.EventID,
						TenantID:   evt.TenantID,
						EventType:  domain.RoutingKeyWorkspaceReady,
						OwnerEmail: evt.OwnerEmail,
						Payload:    d.Body,
					}
					return c.notificationService.ProcessEventAndTrySendWelcome(txCtx, input)
				})

				if err != nil {
					log.Printf("WorkspaceReadyConsumer Error: Failed to handle WorkspaceReady for event '%s': %v", evt.EventID, err)
					_ = d.Nack(false, true) // Requeue
					continue
				}

				_ = d.Ack(false)
				log.Printf("WorkspaceReadyConsumer: Successfully processed & ACKed event_id='%s'", evt.EventID)
			}
		}
	}()

	return nil
}
