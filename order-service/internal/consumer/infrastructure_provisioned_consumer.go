package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"order-service/internal/crypto"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
	"order-service/internal/service"
)

const (
	ExchangeCompanyEvents               = "company.events"
	RoutingKeyInfrastructureProvisioned = "infrastructure.provisioned"
	RoutingKeyTenantOrderDBReady        = "tenant.order_db.ready"
	QueueOrderServiceInfraProvisioned   = "order_service_infrastructure_provisioned"
)

type InfrastructureProvisionedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Plan       string `json:"plan"`
	DBHost     string `json:"db_host"`
	DBPort     int    `json:"db_port"`
	DBName     string `json:"db_name"`
	DBUser     string `json:"db_user"`
	SchemaName string `json:"schema_name"`
}

type TenantOrderDBReadyEvent struct {
	EventID     string `json:"event_id"`
	TenantID    string `json:"tenant_id"`
	ServiceName string `json:"service_name"`
	DBHost      string `json:"db_host"`
	DBPort      int    `json:"db_port"`
	DBName      string `json:"db_name"`
	DBUser      string `json:"db_user"`
	SchemaName  string `json:"schema_name"`
}

type InfrastructureProvisionedConsumer struct {
	client           *rabbitmq.Client
	migrationService service.MigrationService
	registry         *registry.PoolRegistry
	sharedSecret     string
	sharedDBPass     string
}

type InfrastructureProvisionedConsumerParams struct {
	Client           *rabbitmq.Client
	MigrationService service.MigrationService
	Registry         *registry.PoolRegistry
	SharedSecret     string
	SharedDBPass     string
}

func NewInfrastructureProvisionedConsumer(params InfrastructureProvisionedConsumerParams) (*InfrastructureProvisionedConsumer, error) {
	if err := params.Client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange '%s': %w", ExchangeCompanyEvents, err)
	}

	if err := params.Client.DeclareAndBindQueue(
		QueueOrderServiceInfraProvisioned, ExchangeCompanyEvents, RoutingKeyInfrastructureProvisioned,
	); err != nil {
		return nil, fmt.Errorf("failed to bind queue '%s': %w", QueueOrderServiceInfraProvisioned, err)
	}

	pass := params.SharedDBPass
	if pass == "" {
		pass = "postgres"
	}

	return &InfrastructureProvisionedConsumer{
		client:           params.Client,
		migrationService: params.MigrationService,
		registry:         params.Registry,
		sharedSecret:     params.SharedSecret,
		sharedDBPass:     pass,
	}, nil
}

func (c *InfrastructureProvisionedConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		QueueOrderServiceInfraProvisioned,
		"order-service-infra-consumer",
		false, // manual ack
		false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue '%s': %w", QueueOrderServiceInfraProvisioned, err)
	}

	log.Printf("OrderService: Listening for '%s' events on queue '%s'...", RoutingKeyInfrastructureProvisioned, QueueOrderServiceInfraProvisioned)

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

				var evt InfrastructureProvisionedEvent
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

				// 2. Evict any cached pool in order-service registry so the new DSN is loaded dynamically on demand
				c.registry.Evict(evt.TenantID)

				// 3. Emit tenant.order_db.ready event over RabbitMQ (Routing metadata ONLY, NO PASSWORDS)
				readyEvt := TenantOrderDBReadyEvent{
					EventID:     evt.EventID,
					TenantID:    evt.TenantID,
					ServiceName: "order-service",
					DBHost:      evt.DBHost,
					DBPort:      evt.DBPort,
					DBName:      evt.DBName,
					DBUser:      evt.DBUser,
					SchemaName:  evt.SchemaName,
				}

				if err := c.client.PublishEvent(ctx, ExchangeCompanyEvents, RoutingKeyTenantOrderDBReady, readyEvt); err != nil {
					log.Printf("InfrastructureProvisionedConsumer Error: Failed to publish '%s': %v", RoutingKeyTenantOrderDBReady, err)
					_ = d.Nack(false, true)
					continue
				}

				_ = d.Ack(false)
				log.Printf("InfrastructureProvisionedConsumer: Successfully provisioned order DB & published '%s' for tenant='%s'",
					RoutingKeyTenantOrderDBReady, evt.TenantID)
			}
		}
	}()

	return nil
}
