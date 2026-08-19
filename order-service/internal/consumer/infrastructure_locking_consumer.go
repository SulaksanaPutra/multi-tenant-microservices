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

type InfrastructureLockingConsumerParams struct {
	Client          AMQPClient
	RoutingRegistry *registry.RoutingRegistry
}

type InfrastructureLockingConsumer struct {
	client          AMQPClient
	routingRegistry *registry.RoutingRegistry
	queueName       string
}

func NewInfrastructureLockingConsumer(params InfrastructureLockingConsumerParams) *InfrastructureLockingConsumer {
	return &InfrastructureLockingConsumer{
		client:          params.Client,
		routingRegistry: params.RoutingRegistry,
	}
}

func (infrastructureLockingConsumer *InfrastructureLockingConsumer) setupTopology() (string, error) {
	if err := infrastructureLockingConsumer.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return "", fmt.Errorf("failed to declare exchange: %w", err)
	}

	return infrastructureLockingConsumer.client.DeclareAndBindExclusiveQueue(domain.ExchangeCompanyEvents, domain.RoutingKeyInfrastructureLocking)
}

func (infrastructureLockingConsumer *InfrastructureLockingConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := infrastructureLockingConsumer.client.ConnContext()

			err := infrastructureLockingConsumer.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("InfrastructureLockingConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := infrastructureLockingConsumer.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("InfrastructureLockingConsumer: reconnected; re-binding exclusive queue topology...")
		}
	}()

	return nil
}

func (infrastructureLockingConsumer *InfrastructureLockingConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	qName, err := infrastructureLockingConsumer.setupTopology()
	if err != nil {
		return fmt.Errorf("failed to setup topology: %w", err)
	}
	infrastructureLockingConsumer.queueName = qName

	msgs, err := infrastructureLockingConsumer.client.Consume(infrastructureLockingConsumer.queueName, "")
	if err != nil {
		return fmt.Errorf("failed to start consume on '%s': %w", infrastructureLockingConsumer.queueName, err)
	}

	log.Printf("InfrastructureLockingConsumer: listening on exclusive queue '%s'...", infrastructureLockingConsumer.queueName)

	for {
		select {
		case <-appCtx.Done():
			return appCtx.Err()

		case <-connCtx.Done():
			return connCtx.Err()

		case d, ok := <-msgs:
			if !ok {
				return errors.New("delivery channel closed")
			}
			_ = infrastructureLockingConsumer.handleDelivery(appCtx, d)
		}
	}
}

func (infrastructureLockingConsumer *InfrastructureLockingConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	var evt domain.InfrastructureLockingEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("InfrastructureLockingConsumer: bad payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	if evt.TenantID == "" {
		log.Printf("InfrastructureLockingConsumer: missing tenant_id in payload, discarding message.")
		_ = d.Nack(false, false)
		return errors.New("missing tenant_id")
	}

	log.Printf("InfrastructureLockingConsumer: locking tenant='%s' — setting status MIGRATING in RoutingRegistry", evt.TenantID)
	infrastructureLockingConsumer.routingRegistry.SetStatus(evt.TenantID, "MIGRATING")
	_ = d.Ack(false)
	return nil
}
