package consumer

import (
	"context"
	"encoding/json"
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

// TenantInfrastructureService is the consumer-side interface expected by TenantOrderDBReadyConsumer.
type TenantInfrastructureService interface {
	HandleInfrastructureUpdate(ctx context.Context, input service.InfrastructureUpdateInput) error
}

type TenantOrderDBReadyConsumer struct {
	txManager                   TxManager
	client                      *rabbitmq.Client
	tenantInfrastructureService TenantInfrastructureService
}

func NewTenantOrderDBReadyConsumer(txManager TxManager, client *rabbitmq.Client, tenantInfrastructureService TenantInfrastructureService) (*TenantOrderDBReadyConsumer, error) {
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
	}, nil
}

func (c *TenantOrderDBReadyConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		domain.QueueTenantServiceOrderReady,
		"tenant-service-ready-consumer",
		false, // manual ack
		false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue '%s': %w", domain.QueueTenantServiceOrderReady, err)
	}

	log.Printf("TenantService: Listening for '%s' events on queue '%s'...", domain.RoutingKeyTenantOrderDBReady, domain.QueueTenantServiceOrderReady)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("TenantOrderDBReadyConsumer: Context cancelled, shutting down.")
				return
			case d, ok := <-msgs:
				if !ok {
					log.Printf("TenantOrderDBReadyConsumer: Message channel closed.")
					return
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
				err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
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
	}()

	return nil
}
