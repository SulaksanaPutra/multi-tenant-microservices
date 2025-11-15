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

// InfrastructureChangedConsumer handles cache invalidation and materialized view updates when a tenant's infrastructure changes.
// Each replica process declares an exclusive, auto-delete anonymous queue so cache invalidations are broadcast to ALL live replicas.
type InfrastructureChangedConsumer struct {
	client          *rabbitmq.Client
	poolRegistry    *registry.PoolRegistry
	routingRegistry *registry.RoutingRegistry
	queueName       string
}

func NewInfrastructureChangedConsumer(client *rabbitmq.Client, poolReg *registry.PoolRegistry, routingReg *registry.RoutingRegistry) (*InfrastructureChangedConsumer, error) {
	consumer := &InfrastructureChangedConsumer{
		client:          client,
		poolRegistry:    poolReg,
		routingRegistry: routingReg,
	}

	queueName, err := consumer.setupTopology()
	if err != nil {
		return nil, fmt.Errorf("failed to setup topology for InfrastructureChangedConsumer: %w", err)
	}
	consumer.queueName = queueName

	return consumer, nil
}

func (c *InfrastructureChangedConsumer) setupTopology() (string, error) {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return "", fmt.Errorf("failed to declare exchange: %w", err)
	}

	// Declare an exclusive, auto-delete anonymous queue for this specific replica process.
	q, err := c.client.Channel.QueueDeclare(
		"",    // empty string generates unique server-assigned queue name (e.g. amq.gen-12345)
		false, // non-durable
		true,  // auto-delete when connection drops
		true,  // exclusive to this replica connection
		false, // no-wait
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("failed to declare exclusive anonymous queue: %w", err)
	}

	if err := c.client.Channel.QueueBind(q.Name, domain.RoutingKeyInfraChanged, domain.ExchangeCompanyEvents, false, nil); err != nil {
		return "", fmt.Errorf("failed to bind exclusive queue to exchange: %w", err)
	}

	return q.Name, nil
}

// Start launches the consumer lifecycle loop in a background goroutine and returns immediately.
// The loop:
//  1. Runs the message delivery loop until the connection context is cancelled (TCP drop) or app shuts down.
//  2. On connection drop, immediately purges all local caches to prevent split-brain — before any retry sleep fires.
//  3. Blocks with zero CPU on WaitUntilReady() until the driver re-establishes the connection.
//  4. Re-binds the exclusive queue on the fresh channel and resumes consumption.
func (c *InfrastructureChangedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			// Obtain the context tied to the current AMQP connection lifetime.
			// This is always non-nil because NewClient() initializes it before returning.
			connCtx := c.client.ConnContext()

			// Run the delivery loop until the connection drops or the app shuts down.
			err := c.runConsumerLoop(ctx, connCtx)

			// Check if the app itself is shutting down — exit cleanly.
			if ctx.Err() != nil {
				return
			}

			// The connection context was cancelled (TCP socket dropped).
			// Purge IMMEDIATELY — connCtx.Done() fires in watchConnection() before any
			// time.Sleep(2s) retry delay, so this purge happens before any missed messages
			// could be processed by stale routing data.
			log.Printf("InfrastructureChangedConsumer: connection context cancelled (%v); purging all local caches to prevent split-brain", err)
			c.poolRegistry.PurgeAll()
			c.routingRegistry.PurgeAll()

			// Block with zero CPU until the driver signals the connection is ready.
			// WaitUntilReady() parks this goroutine on <-readyCh — the Go scheduler
			// wakes it only when watchConnection() calls close(c.readyCh) after reconnect.
			// No polling, no backoff loop, no CPU burn.
			log.Println("InfrastructureChangedConsumer: waiting for RabbitMQ reconnection...")
			if err := c.client.WaitUntilReady(ctx); err != nil {
				// ctx was cancelled during wait — app is shutting down.
				return
			}

			// Reconnect succeeded. Re-bind the exclusive queue on the fresh channel and resume.
			log.Println("InfrastructureChangedConsumer: reconnected; re-binding exclusive queue topology...")
			qName, err := c.setupTopology()
			if err != nil {
				log.Printf("InfrastructureChangedConsumer: topology setup failed after reconnect: %v; will retry on next cycle", err)
				continue
			}
			c.queueName = qName
			log.Printf("InfrastructureChangedConsumer: successfully re-bound exclusive queue '%s'", c.queueName)
		}
	}()

	return nil
}

// runConsumerLoop starts consuming from the exclusive queue and processes deliveries until
// the connection context is cancelled (TCP drop) or the app context is done (shutdown).
// It returns the cancellation error so the caller can distinguish app shutdown from disconnect.
func (c *InfrastructureChangedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
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

	log.Printf("InfrastructureChangedConsumer: listening on exclusive queue '%s'...", c.queueName)

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

			var evt domain.InfraChangedEvent
			if err := json.Unmarshal(d.Body, &evt); err != nil {
				log.Printf("InfrastructureChangedConsumer: bad payload: %v", err)
				_ = d.Nack(false, false)
				continue
			}

			log.Printf("InfrastructureChangedConsumer: evicting pool & routing metadata for tenant='%s'", evt.TenantID)
			c.poolRegistry.Evict(evt.TenantID)
			c.routingRegistry.Delete(evt.TenantID)
			_ = d.Ack(false)
		}
	}
}
