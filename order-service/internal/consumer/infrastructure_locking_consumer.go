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

// InfrastructureLockingConsumer receives the tenant.infrastructure_locking broadcast event
// and sets the tenant's Status to "MIGRATING" in the local RoutingRegistry.
//
// This consumer uses the Fanout Broadcast pattern (exclusive anonymous queue) so that
// ALL live replicas of order-service receive the lock event simultaneously.
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

func (c *InfrastructureLockingConsumer) setupTopology() (string, error) {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return "", fmt.Errorf("failed to declare exchange: %w", err)
	}

	return c.client.DeclareAndBindExclusiveQueue(domain.ExchangeCompanyEvents, domain.RoutingKeyInfrastructureLocking)
}

// Start launches the consumer lifecycle loop in a background goroutine and returns immediately.
func (c *InfrastructureLockingConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := c.client.ConnContext()

			err := c.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("InfrastructureLockingConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := c.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("InfrastructureLockingConsumer: reconnected; re-binding exclusive queue topology...")
		}
	}()

	return nil
}

func (c *InfrastructureLockingConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	qName, err := c.setupTopology()
	if err != nil {
		return fmt.Errorf("failed to setup topology: %w", err)
	}
	c.queueName = qName

	msgs, err := c.client.Consume(c.queueName, "")
	if err != nil {
		return fmt.Errorf("failed to start consume on '%s': %w", c.queueName, err)
	}

	log.Printf("InfrastructureLockingConsumer: listening on exclusive queue '%s'...", c.queueName)

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
			_ = c.handleDelivery(appCtx, d)
		}
	}
}

func (c *InfrastructureLockingConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
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
	c.routingRegistry.SetStatus(evt.TenantID, "MIGRATING")
	_ = d.Ack(false)
	return nil
}
