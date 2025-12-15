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
	go func() {
		for {
			connCtx := c.client.ConnContext()

			err := c.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("WorkspaceReadyConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := c.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("WorkspaceReadyConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (c *WorkspaceReadyConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := c.client.DeclareAndBindQueue(domain.QueueNotificationWorkspaceReady, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceReady); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	if c.client == nil || c.client.Channel == nil {
		return errors.New("channel is nil")
	}

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
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("NotificationService listening for '%s' events on queue '%s'...", domain.RoutingKeyWorkspaceReady, domain.QueueNotificationWorkspaceReady)

	for {
		select {
		case <-appCtx.Done():
			log.Printf("WorkspaceReadyConsumer: Context cancelled, shutting down.")
			return appCtx.Err()

		case <-connCtx.Done():
			return connCtx.Err()

		case d, ok := <-msgs:
			if !ok {
				return errors.New("delivery channel closed")
			}

			var evt domain.WorkspaceReadyEvent
			if err := json.Unmarshal(d.Body, &evt); err != nil {
				log.Printf("Error unmarshaling WorkspaceReady payload: %v", err)
				_ = d.Nack(false, false)
				continue
			}

			log.Printf("WorkspaceReadyConsumer processing event_id='%s' for tenant_id='%s'", evt.EventID, evt.TenantID)

			err := c.txManager.WithTransaction(appCtx, func(txCtx context.Context) error {
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
}
