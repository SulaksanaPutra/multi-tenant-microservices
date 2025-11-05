package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"order-service/internal/crypto"
	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
	"order-service/internal/service"
)

// OrderDBReadyPublisher is the consumer-side interface expected by InfrastructureProvisionedConsumer.
type OrderDBReadyPublisher interface {
	Publish(ctx context.Context, evt domain.TenantOrderDBReadyEvent) error
}

type InfrastructureProvisionedConsumer struct {
	client           *rabbitmq.Client
	publisher        OrderDBReadyPublisher
	migrationService service.MigrationService
	poolRegistry     *registry.PoolRegistry
	routingRegistry  *registry.RoutingRegistry
	sharedSecret     string
	sharedDBPass     string
}

type InfrastructureProvisionedConsumerParams struct {
	Client           *rabbitmq.Client
	Publisher        OrderDBReadyPublisher
	MigrationService service.MigrationService
	PoolRegistry     *registry.PoolRegistry
	RoutingRegistry  *registry.RoutingRegistry
	SharedSecret     string
	SharedDBPass     string
}

func NewInfrastructureProvisionedConsumer(params InfrastructureProvisionedConsumerParams) (*InfrastructureProvisionedConsumer, error) {
	if err := params.Client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}

	if err := params.Client.DeclareAndBindQueue(
		domain.QueueOrderServiceInfraProvisioned, domain.ExchangeCompanyEvents, domain.RoutingKeyInfrastructureProvisioned,
	); err != nil {
		return nil, fmt.Errorf("failed to bind queue '%s': %w", domain.QueueOrderServiceInfraProvisioned, err)
	}

	pass := params.SharedDBPass
	if pass == "" {
		pass = "postgres"
	}

	return &InfrastructureProvisionedConsumer{
		client:           params.Client,
		publisher:        params.Publisher,
		migrationService: params.MigrationService,
		poolRegistry:     params.PoolRegistry,
		routingRegistry:  params.RoutingRegistry,
		sharedSecret:     params.SharedSecret,
		sharedDBPass:     pass,
	}, nil
}

func (c *InfrastructureProvisionedConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		domain.QueueOrderServiceInfraProvisioned,
		"order-service-infra-consumer",
		false, // manual ack
		false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue '%s': %w", domain.QueueOrderServiceInfraProvisioned, err)
	}

	log.Printf("OrderService: Listening for '%s' events on queue '%s'...", domain.RoutingKeyInfrastructureProvisioned, domain.QueueOrderServiceInfraProvisioned)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("InfrastructureProvisionedConsumer: Context cancelled, shutting down.")
				return
			case d, ok := <-msgs:
				if !ok {
					log.Printf("InfrastructureProvisionedConsumer: Message channel closed.")
					return
				}

				var evt domain.InfrastructureProvisionedEvent
				if err := json.Unmarshal(d.Body, &evt); err != nil {
					log.Printf("InfrastructureProvisionedConsumer Error: Bad payload: %v", err)
					_ = d.Nack(false, false)
					continue
				}

				log.Printf("InfrastructureProvisionedConsumer: Configuring DB & migrations for tenant='%s' plan='%s' host='%s'",
					evt.TenantID, evt.Plan, evt.DBHost)

				var pass string
				if evt.Plan == "dedicated" {
					pass = crypto.DeriveTenantDBPassword(c.sharedSecret, evt.TenantID)
				} else {
					pass = c.sharedDBPass
				}

				dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
					evt.DBHost, evt.DBPort, evt.DBUser, pass, evt.DBName)

				// 1. Run SQL migrations
				if err := c.migrationService.MigrateTenantDB(ctx, dsn, evt.SchemaName); err != nil {
					log.Printf("InfrastructureProvisionedConsumer Error: Migration failed for tenant='%s': %v", evt.TenantID, err)
					_ = d.Nack(false, true) // requeue for retry
					continue
				}

				// 2. Populate/update local RoutingRegistry materialized view
				c.routingRegistry.Set(registry.RoutingMetadata{
					TenantID:   evt.TenantID,
					DBHost:     evt.DBHost,
					DBPort:     evt.DBPort,
					DBName:     evt.DBName,
					DBUser:     evt.DBUser,
					SchemaName: evt.SchemaName,
				})

				// 3. Evict any cached pool in order-service pool registry so fresh connection parameters are used
				c.poolRegistry.Evict(evt.TenantID)

				// 4. Emit tenant.order_db.ready event via dedicated Publisher Adapter
				readyEvt := domain.TenantOrderDBReadyEvent{
					EventID:     evt.EventID,
					TenantID:    evt.TenantID,
					ServiceName: "order-service",
					DBHost:      evt.DBHost,
					DBPort:      evt.DBPort,
					DBName:      evt.DBName,
					DBUser:      evt.DBUser,
					SchemaName:  evt.SchemaName,
				}

				if err := c.publisher.Publish(ctx, readyEvt); err != nil {
					log.Printf("InfrastructureProvisionedConsumer Error: Failed to publish tenant.order_db.ready: %v", err)
					_ = d.Nack(false, true)
					continue
				}

				_ = d.Ack(false)
				log.Printf("InfrastructureProvisionedConsumer: Successfully provisioned order DB & published '%s' for tenant='%s'",
					domain.RoutingKeyTenantOrderDBReady, evt.TenantID)
			}
		}
	}()

	return nil
}
