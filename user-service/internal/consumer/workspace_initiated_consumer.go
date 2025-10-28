package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/service"
	"user-service/internal/txcontext"
)

const (
	ExchangeCompanyEvents              = "company.events"
	RoutingKeyWorkspaceInitiated       = "workspace.initiated"
	QueueUserServiceWorkspaceInitiated = "user_service_workspace_initiated"
)

type WorkspaceInitiatedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Plan       string `json:"plan"`
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
}

// UserService is the consumer-side interface expected by WorkspaceInitiatedConsumer.
type UserService interface {
	CreateUserFromWorkspace(ctx context.Context, input service.CreateUserFromWorkspaceInput) error
}

type WorkspaceInitiatedConsumer struct {
	txManager   txcontext.TxManager
	client      *rabbitmq.Client
	userService UserService
}

func NewWorkspaceInitiatedConsumer(txManager txcontext.TxManager, client *rabbitmq.Client, userService UserService) (*WorkspaceInitiatedConsumer, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := client.DeclareAndBindQueue(QueueUserServiceWorkspaceInitiated, ExchangeCompanyEvents, RoutingKeyWorkspaceInitiated); err != nil {
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	return &WorkspaceInitiatedConsumer{
		txManager:   txManager,
		client:      client,
		userService: userService,
	}, nil
}

func (c *WorkspaceInitiatedConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		QueueUserServiceWorkspaceInitiated, // queue
		"user-service-worker",              // consumer tag
		false,                              // auto-ack
		false,                              // exclusive
		false,                              // no-local
		false,                              // no-wait
		nil,                                // args
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue %s: %w", QueueUserServiceWorkspaceInitiated, err)
	}

	log.Printf("User Service worker listening for events on queue '%s'...", QueueUserServiceWorkspaceInitiated)

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

				var evt WorkspaceInitiatedEvent
				if err := json.Unmarshal(d.Body, &evt); err != nil {
					log.Printf("Error unmarshaling WorkspaceInitiated payload: %v", err)
					err := d.Nack(false, false)
					if err != nil {
						return
					}
					continue
				}

				input := service.CreateUserFromWorkspaceInput{
					EventID:    evt.EventID,
					TenantID:   evt.TenantID,
					OwnerEmail: evt.OwnerEmail,
					OwnerName:  evt.OwnerName,
				}

				err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
					return c.userService.CreateUserFromWorkspace(txCtx, input)
				})

				if err != nil {
					log.Printf("Error creating user from workspace event: %v", err)
					err := d.Nack(false, true)
					if err != nil {
						return
					}
					continue
				}

				err = d.Ack(false)
				if err != nil {
					return
				}
			}
		}
	}()

	return nil
}
