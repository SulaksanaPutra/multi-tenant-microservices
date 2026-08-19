package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
)

type InfrastructureChangedConsumerParams struct {
	Client          AMQPClient
	PoolRegistry    *registry.PoolRegistry
	RoutingRegistry *registry.RoutingRegistry
}

type InfrastructureChangedConsumer struct {
	client          AMQPClient
	poolRegistry    *registry.PoolRegistry
	routingRegistry *registry.RoutingRegistry
	queueName       string
}

func NewInfrastructureChangedConsumer(params InfrastructureChangedConsumerParams) *InfrastructureChangedConsumer {
	return &InfrastructureChangedConsumer{
		client:          params.Client,
		poolRegistry:    params.PoolRegistry,
		routingRegistry: params.RoutingRegistry,
	}
}

func (infrastructureChangedConsumer *InfrastructureChangedConsumer) setupTopology() (string, error) {
	if err := infrastructureChangedConsumer.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return "", fmt.Errorf("failed to declare exchange: %w", err)
	}

	return infrastructureChangedConsumer.client.DeclareAndBindExclusiveQueue(domain.ExchangeCompanyEvents, domain.RoutingKeyInfraChanged)
}

func (infrastructureChangedConsumer *InfrastructureChangedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := infrastructureChangedConsumer.client.ConnContext()

			err := infrastructureChangedConsumer.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("InfrastructureChangedConsumer: connection context cancelled (%v); purging all local caches to prevent split-brain", err)
			infrastructureChangedConsumer.poolRegistry.PurgeAll()
			infrastructureChangedConsumer.routingRegistry.PurgeAll()

			log.Println("InfrastructureChangedConsumer: waiting for RabbitMQ reconnection...")
			if err := infrastructureChangedConsumer.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("InfrastructureChangedConsumer: reconnected; re-binding exclusive queue topology...")
		}
	}()

	return nil
}

func (infrastructureChangedConsumer *InfrastructureChangedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	qName, err := infrastructureChangedConsumer.setupTopology()
	if err != nil {
		return fmt.Errorf("failed to setup topology: %w", err)
	}
	infrastructureChangedConsumer.queueName = qName

	msgs, err := infrastructureChangedConsumer.client.Consume(infrastructureChangedConsumer.queueName, "")
	if err != nil {
		return fmt.Errorf("failed to start consume on '%s': %w", infrastructureChangedConsumer.queueName, err)
	}

	log.Printf("InfrastructureChangedConsumer: listening on exclusive queue '%s'...", infrastructureChangedConsumer.queueName)

	for {
		select {
		case <-appCtx.Done():
			// Application is shutting down — exit cleanly.
			return appCtx.Err()

		case <-connCtx.Done():
			// TCP socket dropped. Return immediately so the caller can purge caches
			// before any reconnect retry sleep in watchConnection() fires.
			return connCtx.Err()

		case d, ok := <-msgs:
			if !ok {
				// Delivery channel closed by the broker — treat as a connection drop.
				return errors.New("delivery channel closed")
			}

			_ = infrastructureChangedConsumer.handleDelivery(appCtx, d)
		}
	}
}

func (infrastructureChangedConsumer *InfrastructureChangedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	var evt domain.InfraChangedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("InfrastructureChangedConsumer: bad payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	log.Printf("InfrastructureChangedConsumer: evicting pool & routing metadata for tenant='%s'", evt.TenantID)
	infrastructureChangedConsumer.poolRegistry.Evict(evt.TenantID)
	infrastructureChangedConsumer.routingRegistry.Delete(evt.TenantID)
	_ = d.Ack(false)
	return nil
}
