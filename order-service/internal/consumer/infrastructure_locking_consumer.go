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
	Client          *rabbitmq.Client
	RoutingRegistry *registry.RoutingRegistry
}

// InfrastructureLockingConsumer receives the tenant.infrastructure_locking broadcast event
// and sets the tenant's Status to "MIGRATING" in the local RoutingRegistry.
//
// This consumer uses the Fanout Broadcast pattern (exclusive anonymous queue) so that
// ALL live replicas of order-service receive the lock event simultaneously.
type InfrastructureLockingConsumer struct {
	client          *rabbitmq.Client
	routingRegistry *registry.RoutingRegistry
	queueName       string
}

func NewInfrastructureLockingConsumer(params InfrastructureLockingConsumerParams) (*InfrastructureLockingConsumer, error) {
	c := &InfrastructureLockingConsumer{
		client:          params.Client,
		routingRegistry: params.RoutingRegistry,
	}

	qName, err := c.setupTopology()
	if err != nil {
		return nil, fmt.Errorf("failed to setup topology for InfrastructureLockingConsumer: %w", err)
	}
	c.queueName = qName

	return c, nil
}

func (c *InfrastructureLockingConsumer) setupTopology() (string, error) {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return "", fmt.Errorf("failed to declare exchange: %w", err)
	}

	// Declare an exclusive, auto-delete anonymous queue for this specific replica process.
	// An empty string name causes RabbitMQ to generate a unique server-assigned queue name
	// (e.g. amq.gen-XXXXX), ensuring the fanout reaches ALL live replicas independently.
	q, err := c.client.Channel.QueueDeclare(
		"",    // empty string → server-assigned unique name (fanout broadcast pattern)
		false, // non-durable
		true,  // auto-delete when connection drops
		true,  // exclusive to this replica connection
		false, // no-wait
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("failed to declare exclusive anonymous queue: %w", err)
	}

	if err := c.client.Channel.QueueBind(q.Name, domain.RoutingKeyInfrastructureLocking, domain.ExchangeCompanyEvents, false, nil); err != nil {
		return "", fmt.Errorf("failed to bind exclusive queue to exchange: %w", err)
	}

	return q.Name, nil
}

// Start launches the consumer lifecycle loop in a background goroutine and returns immediately.
// On connection drop the goroutine purges no caches (the MIGRATING state is sticky until
// an InfraChanged event explicitly clears it) and re-binds the exclusive queue after reconnect.
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
			qName, err := c.setupTopology()
			if err != nil {
				log.Printf("InfrastructureLockingConsumer: topology setup failed after reconnect: %v; will retry on next cycle", err)
				continue
			}
			c.queueName = qName
			log.Printf("InfrastructureLockingConsumer: successfully re-bound exclusive queue '%s'", c.queueName)
		}
	}()

	return nil
}

func (c *InfrastructureLockingConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		c.queueName,
		"",    // auto-generated consumer tag
		false, // autoAck
		true,  // exclusive
		false, // noLocal
		false, // noWait
		nil,
	)
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
