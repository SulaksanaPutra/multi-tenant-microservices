package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/service"
)

// TxManager is the consumer-side interface expected by TenantOrderDBReadyConsumer.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

// InboxRepository is the consumer-side interface expected by TenantOrderDBReadyConsumer.
type InboxRepository interface {
	TryInsert(ctx context.Context, eventID string) (bool, error)
}

// TenantInfrastructureService is the consumer-side interface expected by TenantOrderDBReadyConsumer.
type TenantInfrastructureService interface {
	HandleInfrastructureUpdate(ctx context.Context, input service.InfrastructureUpdateInput) error
}

type TenantOrderDBReadyConsumer struct {
	txManager                   TxManager
	client                      *rabbitmq.Client
	tenantInfrastructureService TenantInfrastructureService
	inboxRepo                   InboxRepository
}

func NewTenantOrderDBReadyConsumer(txManager TxManager, client *rabbitmq.Client, tenantInfrastructureService TenantInfrastructureService, inboxRepo InboxRepository) (*TenantOrderDBReadyConsumer, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}

	if err := client.DeclareAndBindQueue(domain.QueueTenantServiceOrderReady, domain.ExchangeCompanyEvents, domain.RoutingKeyTenantOrderDBReady); err != nil {
		return nil, fmt.Errorf("failed to bind queue '%s': %w", domain.QueueTenantServiceOrderReady, err)
	}

	return &TenantOrderDBReadyConsumer{
		txManager:                   txManager,
		client:                      client,
		tenantInfrastructureService: tenantInfrastructureService,
		inboxRepo:                   inboxRepo,
	}, nil
}

func (c *TenantOrderDBReadyConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := c.client.ConnContext()

			err := c.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("TenantOrderDBReadyConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := c.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("TenantOrderDBReadyConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (c *TenantOrderDBReadyConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}

	if err := c.client.DeclareAndBindQueue(domain.QueueTenantServiceOrderReady, domain.ExchangeCompanyEvents, domain.RoutingKeyTenantOrderDBReady); err != nil {
		return fmt.Errorf("failed to bind queue '%s': %w", domain.QueueTenantServiceOrderReady, err)
	}

	if c.client == nil || c.client.Channel == nil {
		return errors.New("channel is nil")
	}

	msgs, err := c.client.Channel.Consume(
		domain.QueueTenantServiceOrderReady,
		"tenant-service-ready-consumer",
		false, // manual ack
		false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("TenantService: Listening for '%s' events on queue '%s'...", domain.RoutingKeyTenantOrderDBReady, domain.QueueTenantServiceOrderReady)

	for {
		select {
		case <-appCtx.Done():
			log.Printf("TenantOrderDBReadyConsumer: Context cancelled, shutting down.")
			return appCtx.Err()

		case <-connCtx.Done():
			return connCtx.Err()

		case d, ok := <-msgs:
			if !ok {
				return errors.New("delivery channel closed")
			}

			var evt domain.TenantOrderDBReadyEvent
			if err := json.Unmarshal(d.Body, &evt); err != nil {
				log.Printf("TenantOrderDBReadyConsumer Error: Bad payload: %v", err)
				_ = d.Nack(false, false)
				continue
			}

			log.Printf("TenantOrderDBReadyConsumer: Received order DB ready for tenant='%s' service='%s'",
				evt.TenantID, evt.ServiceName)

			// Wrap update handling inside transaction
			err := c.txManager.WithTransaction(appCtx, func(txCtx context.Context) error {
				if c.inboxRepo != nil && evt.EventID != "" {
					isDuplicate, err := c.inboxRepo.TryInsert(txCtx, evt.EventID)
					if err != nil {
						return fmt.Errorf("failed to insert inbox event: %w", err)
					}
					if isDuplicate {
						log.Printf("TenantOrderDBReadyConsumer: Duplicate event_id='%s' detected for tenant='%s', skipping processing.", evt.EventID, evt.TenantID)
						return nil
					}
				}

				input := service.InfrastructureUpdateInput{
					TenantID:    evt.TenantID,
					ServiceName: evt.ServiceName,
					DBHost:      evt.DBHost,
					DBPort:      evt.DBPort,
					DBName:      evt.DBName,
					DBUser:      evt.DBUser,
					SchemaName:  evt.SchemaName,
				}
				return c.tenantInfrastructureService.HandleInfrastructureUpdate(txCtx, input)
			})

			if err != nil {
				log.Printf("TenantOrderDBReadyConsumer Error: Failed to handle infra update for tenant='%s': %v", evt.TenantID, err)
				_ = d.Nack(false, true)
				continue
			}

			_ = d.Ack(false)
			log.Printf("TenantOrderDBReadyConsumer: Successfully processed order DB ready event for tenant='%s'", evt.TenantID)
		}
	}
}
