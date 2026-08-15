package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/service"
)

type WorkspaceInitiatedConsumerParams struct {
	TxManager    TxManager
	Client       AMQPClient
	InboxService InboxService
	UserService  UserService
}

type WorkspaceInitiatedConsumer struct {
	txManager    TxManager
	client       AMQPClient
	inboxService InboxService
	userService  UserService
}

func NewWorkspaceInitiatedConsumer(params WorkspaceInitiatedConsumerParams) *WorkspaceInitiatedConsumer {
	return &WorkspaceInitiatedConsumer{
		txManager:    params.TxManager,
		client:       params.Client,
		inboxService: params.InboxService,
		userService:  params.UserService,
	}
}

func (c *WorkspaceInitiatedConsumer) setupTopology() error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := c.client.DeclareAndBindQueue(domain.QueueUserServiceWorkspaceInitiated, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceInitiated); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	return nil
}

func (c *WorkspaceInitiatedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := c.client.ConnContext()

			err := c.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("WorkspaceInitiatedConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := c.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("WorkspaceInitiatedConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (c *WorkspaceInitiatedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := c.setupTopology(); err != nil {
		return err
	}

	msgs, err := c.client.Consume(
		domain.QueueUserServiceWorkspaceInitiated,
		"user-service-worker",
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("User Service worker listening for events on queue '%s'...", domain.QueueUserServiceWorkspaceInitiated)

	for {
		select {
		case <-appCtx.Done():
			log.Printf("WorkspaceInitiatedConsumer: Context cancelled, shutting down.")
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

func (c *WorkspaceInitiatedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	if d.RoutingKey != domain.RoutingKeyWorkspaceInitiated && d.RoutingKey != "" {
		log.Printf("[WARN] WorkspaceInitiatedConsumer: Received misrouted message with routing_key='%s' (expected '%s'). Discarding.", d.RoutingKey, domain.RoutingKeyWorkspaceInitiated)
		_ = d.Ack(false)
		return nil
	}

	var evt domain.WorkspaceInitiatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("Error unmarshaling WorkspaceInitiated payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	deliveryCount := getDeliveryCount(d.Headers)
	if deliveryCount >= 3 {
		log.Printf("[DLQ] WorkspaceInitiatedConsumer: Max delivery count reached for event_id='%s' tenant_id='%s' (delivery_count=%d). Routing to DLQ.",
			evt.EventID, evt.TenantID, deliveryCount)
		_ = d.Nack(false, false)
		return errors.New("max delivery count reached")
	}

	log.Printf("WorkspaceInitiatedConsumer processing event_id='%s' for tenant_id='%s'", evt.EventID, evt.TenantID)

	err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		isDup, err := c.inboxService.ClaimEvent(txCtx, evt.EventID)
		if err != nil {
			return fmt.Errorf("inbox guard failure: %w", err)
		}
		if isDup {
			log.Printf("WorkspaceInitiatedConsumer: Event '%s' already processed in Inbox guard. Skipping cleanly.", evt.EventID)
			return nil
		}

		input := service.CreateUserFromWorkspaceInput{
			EventID:    evt.EventID,
			TenantID:   evt.TenantID,
			OwnerEmail: evt.OwnerEmail,
			OwnerName:  evt.OwnerName,
		}
		if err := c.userService.CreateUserFromWorkspace(txCtx, input); err != nil {
			return fmt.Errorf("failed to create user profile: %w", err)
		}

		return nil
	})

	if err != nil {
		log.Printf("WorkspaceInitiatedConsumer Error: Transaction failed for event '%s': %v", evt.EventID, err)
		_ = d.Nack(false, true) // Requeue on transient error
		return err
	}

	_ = d.Ack(false)
	log.Printf("WorkspaceInitiatedConsumer: Successfully committed transaction & ACKed message event_id='%s'", evt.EventID)
	return nil
}
