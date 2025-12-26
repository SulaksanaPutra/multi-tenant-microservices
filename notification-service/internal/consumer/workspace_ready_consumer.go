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

type WorkspaceReadyConsumerParams struct {
	TxManager           TxManager
	Client              *rabbitmq.Client
	NotificationService NotificationService
}

type WorkspaceReadyConsumer struct {
	txManager           TxManager
	client              *rabbitmq.Client
	notificationService NotificationService
}

func NewWorkspaceReadyConsumer(params WorkspaceReadyConsumerParams) (*WorkspaceReadyConsumer, error) {
	consumer := &WorkspaceReadyConsumer{
		txManager:           params.TxManager,
		client:              params.Client,
		notificationService: params.NotificationService,
	}

	if err := consumer.setupTopology(); err != nil {
		return nil, err
	}

	return consumer, nil
}

func (c *WorkspaceReadyConsumer) setupTopology() error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := c.client.DeclareAndBindQueue(domain.QueueNotificationWorkspaceReady, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceReady); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	return nil
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
	if err := c.setupTopology(); err != nil {
		return err
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

			_ = c.handleDelivery(appCtx, d)
		}
	}
}

func (c *WorkspaceReadyConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	var evt domain.WorkspaceReadyEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("Error unmarshaling WorkspaceReady payload: %v", err)
		_ = d.Nack(false, false)
		return err
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
		return err
	}

	_ = d.Ack(false)
	log.Printf("WorkspaceReadyConsumer: Successfully processed & ACKed event_id='%s'", evt.EventID)
	return nil
}
