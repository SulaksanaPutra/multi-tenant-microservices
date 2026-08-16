// Package consumer implements the inbound AMQP consumers for auth-service.
package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure/rabbitmq"
	"auth-service/internal/service"
)

type UserCreatedConsumerParams struct {
	TxManager         TxManager
	Client            AMQPClient
	InboxService      InboxService
	MembershipService MembershipService
	// MaxDeliveries caps poison-pill requeues before routing to the DLQ.
	MaxDeliveries int
}

type UserCreatedConsumer struct {
	txManager         TxManager
	client            AMQPClient
	inboxService      InboxService
	membershipService MembershipService
	maxDeliveries     int
}

func NewUserCreatedConsumer(params UserCreatedConsumerParams) *UserCreatedConsumer {
	maxDeliveries := params.MaxDeliveries
	if maxDeliveries <= 0 {
		maxDeliveries = domain.MaxAuthUserCreatedDeliveries
	}

	return &UserCreatedConsumer{
		txManager:         params.TxManager,
		client:            params.Client,
		inboxService:      params.InboxService,
		membershipService: params.MembershipService,
		maxDeliveries:     maxDeliveries,
	}
}

// setupTopology declares the broker-native DLX topology: the main queue is
// bound to company.events on user.created and dead-letters to
// company.events.dlx → auth_service_user_created_membership_dlq.
func (userCreatedConsumer *UserCreatedConsumer) setupTopology() error {
	if err := userCreatedConsumer.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := userCreatedConsumer.client.DeclareExchange(domain.ExchangeCompanyEventsDLX, "topic"); err != nil {
		return fmt.Errorf("failed to declare DLX exchange: %w", err)
	}

	if err := userCreatedConsumer.client.DeclareAndBindQueue(
		domain.QueueAuthUserCreated,
		domain.ExchangeCompanyEvents,
		domain.RoutingKeyUserCreated,
		map[string]interface{}{
			"x-dead-letter-exchange":    domain.ExchangeCompanyEventsDLX,
			"x-dead-letter-routing-key": domain.QueueAuthUserCreatedDLQ,
		},
	); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	if err := userCreatedConsumer.client.DeclareAndBindQueue(
		domain.QueueAuthUserCreatedDLQ,
		domain.ExchangeCompanyEventsDLX,
		domain.QueueAuthUserCreatedDLQ,
		nil,
	); err != nil {
		return fmt.Errorf("failed to declare DLQ: %w", err)
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
		domain.QueueAuthUserCreated,
		"auth-user-created-consumer",
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("AuthService listening for '%s' events on queue '%s'...", domain.RoutingKeyUserCreated, domain.QueueAuthUserCreated)

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
	// Routing-key guard: drain misrouted messages without processing them.
	if d.RoutingKey != domain.RoutingKeyUserCreated && d.RoutingKey != "" {
		log.Printf("[WARN] UserCreatedConsumer: Received misrouted message with routing_key='%s' (expected '%s'). Discarding. Check AMQP queue topology for ghost bindings.", d.RoutingKey, domain.RoutingKeyUserCreated)
		_ = d.Ack(false) // Ack to drain from queue; no valid handler exists on this consumer
		return nil
	}

	var evt domain.UserCreatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("Error unmarshaling UserCreated payload: %v", err)
		_ = d.Nack(false, false) // poison pill -> broker routes to DLQ via DLX
		return err
	}

	// Delivery-count cap: DLQ persistent failures instead of infinite requeues.
	deliveryCount := getDeliveryCount(d.Headers)
	if deliveryCount >= userCreatedConsumer.maxDeliveries {
		log.Printf("[DLQ] UserCreatedConsumer: Max deliveries (%d) reached for event_id='%s' user_id='%s' (delivery_count=%d). Routing to DLQ.",
			userCreatedConsumer.maxDeliveries, evt.EventID, evt.UserID, deliveryCount)
		_ = d.Nack(false, false)
		return errors.New("max delivery count reached")
	}

	log.Printf("UserCreatedConsumer processing event_id='%s' for user_id='%s' tenant_id='%s'", evt.EventID, evt.UserID, evt.TenantID)

	err := userCreatedConsumer.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		inboxInput := service.ClaimInboxInput{
			EventID:   evt.EventID,
			TenantID:  evt.TenantID,
			EventType: domain.RoutingKeyUserCreated,
			Payload:   d.Body,
		}

		// Step 1: transactional inbox guard — deduplicates the event.
		isDup, err := userCreatedConsumer.inboxService.ClaimEvent(txCtx, inboxInput)
		if err != nil {
			return fmt.Errorf("inbox guard failed: %w", err)
		}
		if isDup {
			log.Printf("UserCreatedConsumer: Duplicate event_id='%s' detected by Inbox guard. Skipping.", evt.EventID)
			return nil
		}

		// Step 2: upsert the local user_tenant_memberships copy.
		if err := userCreatedConsumer.membershipService.AddMembership(txCtx, evt.UserID, evt.TenantID); err != nil {
			return fmt.Errorf("failed to upsert membership copy: %w", err)
		}

		return nil
	})

	if err != nil {
		log.Printf("UserCreatedConsumer Error: Transaction failed for event '%s': %v", evt.EventID, err)
		_ = d.Nack(false, true) // Requeue on transient error
		return err
	}

	// Ack only after successful DB commit.
	_ = d.Ack(false)
	log.Printf("UserCreatedConsumer: Successfully committed transaction & ACKed message event_id='%s'", evt.EventID)
	return nil
}
