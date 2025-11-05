package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/service"
	"user-service/internal/txcontext"
)

// UserService is the consumer-side interface expected by WorkspaceInitiatedConsumer.
type UserService interface {
	CreateUserFromWorkspace(ctx context.Context, input service.CreateUserFromWorkspaceInput) error
}

// InboxRepo is the consumer-side interface expected by WorkspaceInitiatedConsumer.
type InboxRepo interface {
	TryInsert(ctx context.Context, eventID string) (bool, error)
}

type WorkspaceInitiatedConsumer struct {
	txManager   txcontext.TxManager
	client      *rabbitmq.Client
	inboxRepo   InboxRepo
	userService UserService
}

func NewWorkspaceInitiatedConsumer(
	txManager txcontext.TxManager,
	client *rabbitmq.Client,
	inboxRepo InboxRepo,
	userService UserService,
) (*WorkspaceInitiatedConsumer, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := client.DeclareAndBindQueue(domain.QueueUserServiceWorkspaceInitiated, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceInitiated); err != nil {
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	return &WorkspaceInitiatedConsumer{
		txManager:   txManager,
		client:      client,
		inboxRepo:   inboxRepo,
		userService: userService,
	}, nil
}

func (c *WorkspaceInitiatedConsumer) Start(ctx context.Context) error {
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
		return fmt.Errorf("failed to consume from queue %s: %w", domain.QueueUserServiceWorkspaceInitiated, err)
	}

	log.Printf("User Service worker listening for events on queue '%s'...", domain.QueueUserServiceWorkspaceInitiated)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("WorkspaceInitiatedConsumer: Context cancelled, shutting down.")
				return
			case d, ok := <-msgs:
				if !ok {
					log.Printf("WorkspaceInitiatedConsumer: Message channel closed.")
					return
				}

				var evt domain.WorkspaceInitiatedEvent
				if err := json.Unmarshal(d.Body, &evt); err != nil {
					log.Printf("Error unmarshaling WorkspaceInitiated payload: %v", err)
					err := d.Nack(false, false)
					if err != nil {
						return
					}
					continue
				}

				log.Printf("WorkspaceInitiatedConsumer processing event_id='%s' for tenant_id='%s'", evt.EventID, evt.TenantID)

				// Wrap Consumer execution inside Unit of Work (Transaction boundary)
				err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
					// 1. Transactional Inbox Guard
					isDup, err := c.inboxRepo.TryInsert(txCtx, evt.EventID)
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
					continue
				}

				// Ack message on RabbitMQ only after successful DB commit
				_ = d.Ack(false)
				log.Printf("WorkspaceInitiatedConsumer: Successfully committed transaction & ACKed message event_id='%s'", evt.EventID)
			}
		}
	}()

	return nil
}
