package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"notification-service/internal/domain"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/repository"
	"notification-service/internal/service"
)

// TxManager is the consumer-side interface expected by UserCreatedConsumer.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

// NotificationService is the consumer-side interface expected by UserCreatedConsumer.
type NotificationService interface {
	ProcessEventAndTrySendWelcome(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error)
}

// InboxService is the consumer-side interface expected by UserCreatedConsumer.
type InboxService interface {
	ClaimEvent(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error)
	GetBarrierEvents(txCtx context.Context, tenantID string) ([]domain.InboxMessage, error)
}

// Mailer is the consumer-side interface expected by UserCreatedConsumer.
type Mailer interface {
	SendWelcomeEmail(recipientEmail, tenantID string) (string, string, error)
}

type UserCreatedConsumerParams struct {
	TxManager           TxManager
	Client              *rabbitmq.Client
	InboxService        InboxService
	NotificationService NotificationService
	Mailer              Mailer
}

type UserCreatedConsumer struct {
	txManager           TxManager
	client              *rabbitmq.Client
	inboxService        InboxService
	notificationService NotificationService
	mailer              Mailer
}

func NewUserCreatedConsumer(params UserCreatedConsumerParams) (*UserCreatedConsumer, error) {
	consumer := &UserCreatedConsumer{
		txManager:           params.TxManager,
		client:              params.Client,
		inboxService:        params.InboxService,
		notificationService: params.NotificationService,
		mailer:              params.Mailer,
	}

	if err := consumer.setupTopology(); err != nil {
		return nil, err
	}

	return consumer, nil
}

func (c *UserCreatedConsumer) setupTopology() error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := c.client.DeclareAndBindQueue(domain.QueueNotificationUserCreated, domain.ExchangeCompanyEvents, domain.RoutingKeyUserCreated); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	return nil
}

func (c *UserCreatedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := c.client.ConnContext()

			err := c.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("UserCreatedConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := c.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("UserCreatedConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (c *UserCreatedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := c.setupTopology(); err != nil {
		return err
	}

	if c.client == nil || c.client.Channel == nil {
		return errors.New("channel is nil")
	}

	msgs, err := c.client.Channel.Consume(
		domain.QueueNotificationUserCreated, // queue
		"notification-user-created-consumer", // consumer tag
		false,                                // auto-ack
		false,                                // exclusive
		false,                                // no-local
		false,                                // no-wait
		nil,                                  // args
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("NotificationService listening for '%s' events on queue '%s'...", domain.RoutingKeyUserCreated, domain.QueueNotificationUserCreated)

	for {
		select {
		case <-appCtx.Done():
			log.Printf("UserCreatedConsumer: Context cancelled, shutting down.")
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

func (c *UserCreatedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	var evt domain.UserCreatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("Error unmarshaling UserCreated payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	log.Printf("UserCreatedConsumer processing event_id='%s' for user_id='%s' email='%s'", evt.EventID, evt.UserID, evt.Email)

	// Phase 1: DB-only work inside the transaction boundary.
	// — Inbox guard (ClaimEvent) and barrier read (GetBarrierEvents) are Layer 1 responsibilities.
	// — NotificationService writes the pending audit log and returns dispatch details.
	// — No external I/O (SMTP, HTTP) is allowed inside this closure.
	var sendDetails *service.ProcessEventOutput
	err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		inboxInput := repository.CreateInboxMessageInput{
			EventID:   evt.EventID,
			TenantID:  evt.TenantID,
			EventType: domain.RoutingKeyUserCreated,
			Payload:   d.Body,
		}

		// Step 1: Transactional inbox guard — deduplicates the event atomically.
		isDup, err := c.inboxService.ClaimEvent(txCtx, inboxInput)
		if err != nil {
			return fmt.Errorf("inbox guard failed: %w", err)
		}
		if isDup {
			log.Printf("UserCreatedConsumer: Duplicate event_id='%s' detected by Inbox guard. Skipping.", evt.EventID)
			return nil
		}

		// Step 2: Read the full barrier state for this tenant (consistent inside the tx).
		events, err := c.inboxService.GetBarrierEvents(txCtx, evt.TenantID)
		if err != nil {
			return fmt.Errorf("failed to fetch barrier events: %w", err)
		}

		// Step 3: Evaluate barrier and persist pending audit log if conditions are met.
		input := service.ProcessEventInput{
			EventID:    evt.EventID,
			UserID:     evt.UserID,
			TenantID:   evt.TenantID,
			EventType:  domain.RoutingKeyUserCreated,
			OwnerEmail: evt.Email,
			Payload:    d.Body,
		}
		details, err := c.notificationService.ProcessEventAndTrySendWelcome(txCtx, input, events)
		if err != nil {
			return err
		}
		sendDetails = details
		return nil
	})

	if err != nil {
		log.Printf("UserCreatedConsumer Error: Failed to handle UserCreated for event '%s': %v", evt.EventID, err)
		_ = d.Nack(false, true) // Requeue
		return err
	}

	// Phase 2: Dispatch email AFTER the transaction commits.
	// DB connection is released; SMTP timeout cannot hold DB locks or cause rollback.
	if sendDetails != nil {
		if _, _, mailErr := c.mailer.SendWelcomeEmail(sendDetails.RecipientEmail, sendDetails.TenantID); mailErr != nil {
			log.Printf("UserCreatedConsumer: SMTP dispatch failed for event_id='%s' recipient='%s': %v — NACKing for retry.",
				evt.EventID, sendDetails.RecipientEmail, mailErr)
			_ = d.Nack(false, true) // Requeue — inbox ON CONFLICT ensures idempotent retry
			return mailErr
		}
		log.Printf("UserCreatedConsumer: Welcome email dispatched to '%s' for tenant='%s'", sendDetails.RecipientEmail, sendDetails.TenantID)
	}

	_ = d.Ack(false)
	log.Printf("UserCreatedConsumer: Successfully processed & ACKed event_id='%s'", evt.EventID)
	return nil
}
