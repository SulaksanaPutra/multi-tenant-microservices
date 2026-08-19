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
	Client              AMQPClient
	InboxService        InboxService
	NotificationService NotificationService
	AuthClient          AuthClient
	Mailer              Mailer
}

type WorkspaceReadyConsumer struct {
	txManager           TxManager
	client              AMQPClient
	inboxService        InboxService
	notificationService NotificationService
	authClient          AuthClient
	mailer              Mailer
}

func NewWorkspaceReadyConsumer(params WorkspaceReadyConsumerParams) *WorkspaceReadyConsumer {
	return &WorkspaceReadyConsumer{
		txManager:           params.TxManager,
		client:              params.Client,
		inboxService:        params.InboxService,
		notificationService: params.NotificationService,
		authClient:          params.AuthClient,
		mailer:              params.Mailer,
	}
}

func (workspaceReadyConsumer *WorkspaceReadyConsumer) setupTopology() error {
	if err := workspaceReadyConsumer.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := workspaceReadyConsumer.client.DeclareAndBindQueue(domain.QueueNotificationWorkspaceReady, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceReady); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	return nil
}

func (workspaceReadyConsumer *WorkspaceReadyConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := workspaceReadyConsumer.client.ConnContext()

			err := workspaceReadyConsumer.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("WorkspaceReadyConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := workspaceReadyConsumer.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("WorkspaceReadyConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (workspaceReadyConsumer *WorkspaceReadyConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := workspaceReadyConsumer.setupTopology(); err != nil {
		return err
	}

	msgs, err := workspaceReadyConsumer.client.Consume(
		domain.QueueNotificationWorkspaceReady,
		"notification-workspace-ready-consumer",
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

			_ = workspaceReadyConsumer.handleDelivery(appCtx, d)
		}
	}
}

func (workspaceReadyConsumer *WorkspaceReadyConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	if d.RoutingKey != domain.RoutingKeyWorkspaceReady && d.RoutingKey != "" {
		log.Printf("[WARN] WorkspaceReadyConsumer: Received misrouted message with routing_key='%s' (expected '%s'). Discarding.", d.RoutingKey, domain.RoutingKeyWorkspaceReady)
		_ = d.Ack(false)
		return nil
	}

	var evt domain.WorkspaceReadyEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("Error unmarshaling WorkspaceReady payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	deliveryCount := getDeliveryCount(d.Headers)
	if deliveryCount >= 3 {
		log.Printf("[DLQ] WorkspaceReadyConsumer: Max delivery count reached for event_id='%s' tenant_id='%s' (delivery_count=%d). Routing to DLQ.",
			evt.EventID, evt.TenantID, deliveryCount)
		_ = d.Nack(false, false)
		return errors.New("max delivery count reached")
	}

	log.Printf("WorkspaceReadyConsumer processing event_id='%s' for tenant_id='%s'", evt.EventID, evt.TenantID)

	var sendDetails *service.ProcessEventOutput
	err := workspaceReadyConsumer.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		inboxInput := service.ClaimInboxInput{
			EventID:   evt.EventID,
			TenantID:  evt.TenantID,
			EventType: domain.RoutingKeyWorkspaceReady,
			Payload:   d.Body,
		}

		isDup, err := workspaceReadyConsumer.inboxService.ClaimEvent(txCtx, inboxInput)
		if err != nil {
			return fmt.Errorf("inbox guard failed: %w", err)
		}
		if isDup {
			alreadySent, err := workspaceReadyConsumer.notificationService.HasSentNotification(txCtx, evt.TenantID)
			if err != nil {
				return fmt.Errorf("failed checking welcome email sent status for duplicate event_id='%s': %w", evt.EventID, err)
			}
			if alreadySent {
				log.Printf("WorkspaceReadyConsumer: Duplicate event_id='%s' detected and welcome email already sent. Skipping.", evt.EventID)
				return nil
			}
			log.Printf("WorkspaceReadyConsumer: Duplicate event_id='%s' detected, but welcome email is not yet sent. Resuming barrier check.", evt.EventID)
		}

		events, err := workspaceReadyConsumer.inboxService.ListBarrierEvents(txCtx, evt.TenantID)
		if err != nil {
			return fmt.Errorf("failed to fetch barrier events: %w", err)
		}

		input := service.ProcessEventInput{
			EventID:    evt.EventID,
			TenantID:   evt.TenantID,
			EventType:  domain.RoutingKeyWorkspaceReady,
			OwnerEmail: evt.OwnerEmail,
			Payload:    d.Body,
		}
		details, err := workspaceReadyConsumer.notificationService.ProcessEventAndTrySendWelcome(txCtx, input, events)
		if err != nil {
			return err
		}
		sendDetails = details
		return nil
	})

	if err != nil {
		log.Printf("WorkspaceReadyConsumer Error: Failed to handle WorkspaceReady for event '%s': %v", evt.EventID, err)
		_ = d.Nack(false, true)
		return err
	}

	if sendDetails != nil {
		setupToken, fetchErr := workspaceReadyConsumer.authClient.GetSetupToken(ctx, sendDetails.UserID, sendDetails.TenantID, sendDetails.RecipientEmail)
		if fetchErr != nil {
			log.Printf("WorkspaceReadyConsumer: Failed to fetch setup token from auth-service for tenant='%s': %v — NACKing for retry.", sendDetails.TenantID, fetchErr)
			_ = d.Nack(false, true)
			return fetchErr
		}

		if _, _, mailErr := workspaceReadyConsumer.mailer.SendWelcomeEmail(sendDetails.RecipientEmail, sendDetails.TenantID, sendDetails.TenantName, sendDetails.TenantSlug, sendDetails.OwnerName, setupToken); mailErr != nil {
			log.Printf("WorkspaceReadyConsumer: SMTP dispatch failed for event_id='%s' recipient='%s': %v — NACKing for retry.",
				evt.EventID, sendDetails.RecipientEmail, mailErr)
			_ = d.Nack(false, true)
			return mailErr
		}
		log.Printf("WorkspaceReadyConsumer: Welcome email dispatched to '%s' for tenant='%s'", sendDetails.RecipientEmail, sendDetails.TenantID)

		if updateErr := workspaceReadyConsumer.notificationService.UpdateNotificationStatus(ctx, sendDetails.LogID, "sent"); updateErr != nil {
			log.Printf("WorkspaceReadyConsumer: Failed updating status to 'sent' for log id=%s: %v", sendDetails.LogID, updateErr)
		}
	}

	_ = d.Ack(false)
	log.Printf("WorkspaceReadyConsumer: Successfully processed & ACKed event_id='%s'", evt.EventID)
	return nil
}
