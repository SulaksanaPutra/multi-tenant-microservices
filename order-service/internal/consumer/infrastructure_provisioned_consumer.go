package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"order-service/internal/crypto"
	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
)

type InfrastructureProvisionedConsumer struct {
	client                AMQPClient
	orderDBReadyPublisher OrderDBReadyPublisher
	migrationService      MigrationService
	poolRegistry          *registry.PoolRegistry
	routingRegistry       *registry.RoutingRegistry
	sharedSecret          string
	sharedDBPass          string
}

type InfrastructureProvisionedConsumerParams struct {
	Client           AMQPClient
	Publisher        OrderDBReadyPublisher
	MigrationService MigrationService
	PoolRegistry     *registry.PoolRegistry
	RoutingRegistry  *registry.RoutingRegistry
	SharedSecret     string
	SharedDBPass     string
}

func NewInfrastructureProvisionedConsumer(params InfrastructureProvisionedConsumerParams) *InfrastructureProvisionedConsumer {
	pass := params.SharedDBPass
	if pass == "" {
		pass = "postgres"
	}

	return &InfrastructureProvisionedConsumer{
		client:                params.Client,
		orderDBReadyPublisher: params.Publisher,
		migrationService:      params.MigrationService,
		poolRegistry:          params.PoolRegistry,
		routingRegistry:       params.RoutingRegistry,
		sharedSecret:          params.SharedSecret,
		sharedDBPass:          pass,
	}
}

func (infrastructureProvisionedConsumer *InfrastructureProvisionedConsumer) setupTopology() error {
	if err := infrastructureProvisionedConsumer.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}

	if err := infrastructureProvisionedConsumer.client.DeclareAndBindQueue(
		domain.QueueOrderServiceInfraProvisioned, domain.ExchangeCompanyEvents, domain.RoutingKeyInfrastructureProvisioned,
	); err != nil {
		return fmt.Errorf("failed to bind queue '%s': %w", domain.QueueOrderServiceInfraProvisioned, err)
	}

	return nil
}

func (infrastructureProvisionedConsumer *InfrastructureProvisionedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := infrastructureProvisionedConsumer.client.ConnContext()

			err := infrastructureProvisionedConsumer.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("InfrastructureProvisionedConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := infrastructureProvisionedConsumer.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("InfrastructureProvisionedConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (infrastructureProvisionedConsumer *InfrastructureProvisionedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := infrastructureProvisionedConsumer.setupTopology(); err != nil {
		return fmt.Errorf("failed to setup topology: %w", err)
	}

	msgs, err := infrastructureProvisionedConsumer.client.Consume(
		domain.QueueOrderServiceInfraProvisioned,
		"order-service-infra-consumer",
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("OrderService: Listening for '%s' events on queue '%s'...", domain.RoutingKeyInfrastructureProvisioned, domain.QueueOrderServiceInfraProvisioned)

	for {
		select {
		case <-appCtx.Done():
			log.Printf("InfrastructureProvisionedConsumer: Context cancelled, shutting down.")
			return appCtx.Err()

		case <-connCtx.Done():
			return connCtx.Err()

		case d, ok := <-msgs:
			if !ok {
				return errors.New("delivery channel closed")
			}

			_ = infrastructureProvisionedConsumer.handleDelivery(appCtx, d)
		}
	}
}

func (infrastructureProvisionedConsumer *InfrastructureProvisionedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	var evt domain.InfrastructureProvisionedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("InfrastructureProvisionedConsumer Error: Bad payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	if strings.TrimSpace(evt.TenantID) == "" {
		log.Printf("InfrastructureProvisionedConsumer Error: Missing tenant_id in payload, discarding message.")
		_ = d.Nack(false, false)
		return errors.New("missing tenant_id in payload")
	}

	// Check delivery count to prevent infinite poison pill retry loops
	deliveryCount := getDeliveryCount(d.Headers)
	if deliveryCount >= 3 {
		log.Printf("InfrastructureProvisionedConsumer Warning: Max retries (3) reached for tenant='%s' event_id='%s' (delivery_count=%d). Routing directly to DLQ.",
			evt.TenantID, evt.EventID, deliveryCount)
		_ = d.Nack(false, false)
		return errors.New("max delivery count reached")
	}

	log.Printf("InfrastructureProvisionedConsumer: Configuring DB & migrations for tenant='%s' plan='%s' host='%s'",
		evt.TenantID, evt.Plan, evt.DBHost)

	var pass string
	if evt.Plan == "dedicated" {
		pass = crypto.DeriveTenantDBPassword(infrastructureProvisionedConsumer.sharedSecret, evt.TenantID)
	} else {
		pass = infrastructureProvisionedConsumer.sharedDBPass
	}

	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		evt.DBHost, evt.DBPort, evt.DBUser, pass, evt.DBName)

	// 1. Run SQL migrations
	if err := infrastructureProvisionedConsumer.migrationService.MigrateTenantDB(ctx, dsn, evt.SchemaName); err != nil {
		log.Printf("InfrastructureProvisionedConsumer Error: Migration failed for tenant='%s': %v", evt.TenantID, err)
		_ = d.Nack(false, true) // requeue for retry
		return err
	}

	// 2. Populate/update local RoutingRegistry materialized view
	infrastructureProvisionedConsumer.routingRegistry.Set(registry.RoutingMetadata{
		TenantID:   evt.TenantID,
		DBHost:     evt.DBHost,
		DBPort:     evt.DBPort,
		DBName:     evt.DBName,
		DBUser:     evt.DBUser,
		SchemaName: evt.SchemaName,
		Status:     "active",
	})

	// 3. Evict any cached pool in order-service pool registry, so fresh connection parameters are used
	infrastructureProvisionedConsumer.poolRegistry.Evict(evt.TenantID)

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

	if err := infrastructureProvisionedConsumer.orderDBReadyPublisher.PublishTenantOrderDBReady(ctx, readyEvt); err != nil {
		log.Printf("InfrastructureProvisionedConsumer Error: Failed to publish tenant.order_db.ready: %v", err)
		_ = d.Nack(false, true)
		return err
	}

	_ = d.Ack(false)
	log.Printf("InfrastructureProvisionedConsumer: Successfully provisioned order DB & published '%s' for tenant='%s'",
		domain.RoutingKeyTenantOrderDBReady, evt.TenantID)
	return nil
}

func getDeliveryCount(headers map[string]any) int {
	if headers == nil {
		return 0
	}

	// 1. Quorum Queues (x-delivery-count header)
	if count, ok := headers["x-delivery-count"]; ok {
		switch v := count.(type) {
		case int:
			return v
		case int32:
			return int(v)
		case int64:
			return int(v)
		}
	}

	// 2. Classic Queues DLX (x-death array header fallback)
	if xDeath, ok := headers["x-death"].([]any); ok && len(xDeath) > 0 {
		if deathMap, ok := xDeath[0].(map[string]any); ok {
			if count, ok := deathMap["count"]; ok {
				switch v := count.(type) {
				case int:
					return v
				case int32:
					return int(v)
				case int64:
					return int(v)
				}
			}
		}
	}

	return 0
}
