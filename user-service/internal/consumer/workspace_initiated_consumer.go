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

// TxManager is the consumer-side interface expected by WorkspaceInitiatedConsumer.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

// UserService is the consumer-side interface expected by WorkspaceInitiatedConsumer.
type UserService interface {
	CreateUserFromWorkspace(ctx context.Context, input service.CreateUserFromWorkspaceInput) error
}

// InboxService is the consumer-side interface expected by WorkspaceInitiatedConsumer.
type InboxService interface {
	ClaimEvent(txCtx context.Context, eventID string) (bool, error)
}

type WorkspaceInitiatedConsumerParams struct {
	TxManager    TxManager
	Client       *rabbitmq.Client
	InboxService InboxService
	UserService  UserService
}

type WorkspaceInitiatedConsumer struct {
	txManager    TxManager
	client       *rabbitmq.Client
	inboxService InboxService
	userService  UserService
}

func NewWorkspaceInitiatedConsumer(params WorkspaceInitiatedConsumerParams) (*WorkspaceInitiatedConsumer, error) {
	if params.InboxService == nil {
		return nil, errors.New("inboxService is required")
	}

	consumer := &WorkspaceInitiatedConsumer{
		txManager:    params.TxManager,
		client:       params.Client,
		inboxService: params.InboxService,
		userService:  params.UserService,
	}

	if err := consumer.setupTopology(); err != nil {
		return nil, err
	}

	return consumer, nil
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

	if c.client == nil || c.client.Channel == nil {
		return errors.New("channel is nil")
	}

	msgs, err := c.client.Channel.Consume(
		domain.QueueUserServiceWorkspaceInitiated, // queue
		"user-service-worker",                     // consumer tag
		false,                                     // auto-ack
		false,                                     // exclusive
		false,                                     // no-local
		false,                                     // no-wait
		nil,                                       // args
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
	var evt domain.WorkspaceInitiatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("Error unmarshaling WorkspaceInitiated payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	log.Printf("WorkspaceInitiatedConsumer processing event_id='%s' for tenant_id='%s'", evt.EventID, evt.TenantID)

	// Wrap Consumer execution inside Unit of Work (Transaction boundary)
	err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		// 1. Transactional Inbox Guard via InboxService
		isDup, err := c.inboxService.ClaimEvent(txCtx, evt.EventID)
		if err != nil {
			return fmt.Errorf("inbox guard failure: %w", err)
		}
		if isDup {
			log.Printf("WorkspaceInitiatedConsumer: Event '%s' already processed in Inbox guard. Skipping cleanly.", evt.EventID)
			return nil
		}

		// 2. Execute Domain logic (Create user profile + Outbox event inside same transaction)
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

	// Ack message on RabbitMQ only after successful DB commit
	_ = d.Ack(false)
	log.Printf("WorkspaceInitiatedConsumer: Successfully committed transaction & ACKed message event_id='%s'", evt.EventID)
	return nil
}
