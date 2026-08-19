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

type UserCreatedConsumerParams struct {
	TxManager           TxManager
	Client              AMQPClient
	InboxService        InboxService
	NotificationService NotificationService
	AuthClient          AuthClient
	Mailer              Mailer
}

type UserCreatedConsumer struct {
	txManager           TxManager
	client              AMQPClient
	inboxService        InboxService
	notificationService NotificationService
	authClient          AuthClient
	mailer              Mailer
}

func NewUserCreatedConsumer(params UserCreatedConsumerParams) *UserCreatedConsumer {
	return &UserCreatedConsumer{
		txManager:           params.TxManager,
		client:              params.Client,
		inboxService:        params.InboxService,
		notificationService: params.NotificationService,
		authClient:          params.AuthClient,
		mailer:              params.Mailer,
	}
}

func (userCreatedConsumer *UserCreatedConsumer) setupTopology() error {
	if err := userCreatedConsumer.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := userCreatedConsumer.client.DeclareAndBindQueue(domain.QueueNotificationUserCreated, domain.ExchangeCompanyEvents, domain.RoutingKeyUserCreated); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	return nil
}

func (userCreatedConsumer *UserCreatedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := userCreatedConsumer.client.ConnContext()

			err := userCreatedConsumer.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("UserCreatedConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := userCreatedConsumer.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("UserCreatedConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (userCreatedConsumer *UserCreatedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := userCreatedConsumer.setupTopology(); err != nil {
		return err
	}

	msgs, err := userCreatedConsumer.client.Consume(
		domain.QueueNotificationUserCreated,
		"notification-user-created-consumer",
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

			_ = userCreatedConsumer.handleDelivery(appCtx, d)
		}
	}
}

func (userCreatedConsumer *UserCreatedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	if d.RoutingKey != domain.RoutingKeyUserCreated && d.RoutingKey != "" {
		log.Printf("[WARN] UserCreatedConsumer: Received misrouted message with routing_key='%s' (expected '%s'). Discarding.", d.RoutingKey, domain.RoutingKeyUserCreated)
		_ = d.Ack(false)
		return nil
	}

	var evt domain.UserCreatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("Error unmarshaling UserCreated payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	deliveryCount := getDeliveryCount(d.Headers)
	if deliveryCount >= 3 {
		log.Printf("[DLQ] UserCreatedConsumer: Max delivery count reached for event_id='%s' user_id='%s' (delivery_count=%d). Routing to DLQ.",
			evt.EventID, evt.UserID, deliveryCount)
		_ = d.Nack(false, false)
		return errors.New("max delivery count reached")
	}

	log.Printf("UserCreatedConsumer processing event_id='%s' for user_id='%s' email='%s'", evt.EventID, evt.UserID, evt.Email)

	var sendDetails *service.ProcessEventOutput
	err := userCreatedConsumer.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		inboxInput := service.ClaimInboxInput{
			EventID:   evt.EventID,
			TenantID:  evt.TenantID,
			EventType: domain.RoutingKeyUserCreated,
			Payload:   d.Body,
		}

		isDup, err := userCreatedConsumer.inboxService.ClaimEvent(txCtx, inboxInput)
		if err != nil {
			return fmt.Errorf("inbox guard failed: %w", err)
		}
		if isDup {
			alreadySent, err := userCreatedConsumer.notificationService.HasSentNotification(txCtx, evt.TenantID)
			if err != nil {
				return fmt.Errorf("failed checking welcome email sent status for duplicate event_id='%s': %w", evt.EventID, err)
			}
			if alreadySent {
				log.Printf("UserCreatedConsumer: Duplicate event_id='%s' detected and welcome email already sent. Skipping.", evt.EventID)
				return nil
			}
			log.Printf("UserCreatedConsumer: Duplicate event_id='%s' detected, but welcome email is not yet sent. Resuming barrier check.", evt.EventID)
		}

		events, err := userCreatedConsumer.inboxService.ListBarrierEvents(txCtx, evt.TenantID)
		if err != nil {
			return fmt.Errorf("failed to fetch barrier events: %w", err)
		}

		input := service.ProcessEventInput{
			EventID:    evt.EventID,
			UserID:     evt.UserID,
			TenantID:   evt.TenantID,
			EventType:  domain.RoutingKeyUserCreated,
			OwnerEmail: evt.Email,
			Payload:    d.Body,
		}
		details, err := userCreatedConsumer.notificationService.ProcessEventAndTrySendWelcome(txCtx, input, events)
		if err != nil {
			return err
		}
		sendDetails = details
		return nil
	})

	if err != nil {
		log.Printf("UserCreatedConsumer Error: Failed to handle UserCreated for event '%s': %v", evt.EventID, err)
		_ = d.Nack(false, true)
		return err
	}

	if sendDetails != nil {
		setupToken, fetchErr := userCreatedConsumer.authClient.FetchSetupToken(ctx, sendDetails.UserID, sendDetails.TenantID, sendDetails.RecipientEmail)
		if fetchErr != nil {
			log.Printf("UserCreatedConsumer: Failed to fetch setup token from auth-service for tenant='%s': %v — NACKing for retry.", sendDetails.TenantID, fetchErr)
			_ = d.Nack(false, true)
			return fetchErr
		}

		if _, _, mailErr := userCreatedConsumer.mailer.SendWelcomeEmail(sendDetails.RecipientEmail, sendDetails.TenantID, sendDetails.TenantName, sendDetails.TenantSlug, sendDetails.OwnerName, setupToken); mailErr != nil {
			log.Printf("UserCreatedConsumer: SMTP dispatch failed for event_id='%s' recipient='%s': %v — NACKing for retry.",
				evt.EventID, sendDetails.RecipientEmail, mailErr)
			_ = d.Nack(false, true)
			return mailErr
		}
		log.Printf("UserCreatedConsumer: Welcome email dispatched to '%s' for tenant='%s'", sendDetails.RecipientEmail, sendDetails.TenantID)

		if updateErr := userCreatedConsumer.notificationService.UpdateNotificationStatus(ctx, sendDetails.LogID, "sent"); updateErr != nil {
			log.Printf("UserCreatedConsumer: Failed updating status to 'sent' for log id=%s: %v", sendDetails.LogID, updateErr)
		}
	}

	_ = d.Ack(false)
	log.Printf("UserCreatedConsumer: Successfully processed & ACKed event_id='%s'", evt.EventID)
	return nil
}
