package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
)

// InfraChangedConsumer handles cache invalidation and materialized view updates when a tenant's infrastructure changes.
// Each replica process declares an exclusive, auto-delete anonymous queue so cache invalidations are broadcast to ALL live replicas.
type InfraChangedConsumer struct {
	client          *rabbitmq.Client
	poolRegistry    *registry.PoolRegistry
	routingRegistry *registry.RoutingRegistry
	queueName       string
}

func NewInfraChangedConsumer(client *rabbitmq.Client, poolReg *registry.PoolRegistry, routingReg *registry.RoutingRegistry) (*InfraChangedConsumer, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	// Declare an exclusive, auto-delete anonymous queue for this specific replica process
	q, err := client.Channel.QueueDeclare(
		"",    // empty string generates unique server-assigned queue name (e.g. amq.gen-12345)
		false, // non-durable
		true,  // auto-delete when connection drops
		true,  // exclusive to this replica connection
		false, // no-wait
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to declare exclusive anonymous queue: %w", err)
	}

	if err := client.Channel.QueueBind(q.Name, domain.RoutingKeyInfraChanged, domain.ExchangeCompanyEvents, false, nil); err != nil {
		return nil, fmt.Errorf("failed to bind exclusive queue to exchange: %w", err)
	}

	return &InfraChangedConsumer{
		client:          client,
		poolRegistry:    poolReg,
		routingRegistry: routingReg,
		queueName:       q.Name,
	}, nil
}

func (c *InfraChangedConsumer) Start(ctx context.Context) error {
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
		return fmt.Errorf("failed to consume from queue '%s': %w", c.queueName, err)
	}

	log.Printf("OrderService: Listening for tenant.infrastructure_changed broadcast events on exclusive queue '%s'...", c.queueName)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case d, ok := <-msgs:
				if !ok {
					return
				}

				var evt domain.InfraChangedEvent
				if err := json.Unmarshal(d.Body, &evt); err != nil {
					log.Printf("InfraChangedConsumer Error: Bad payload: %v", err)
					_ = d.Nack(false, false)
					continue
				}

				log.Printf("InfraChangedConsumer: Evicting pool & resetting routing metadata for tenant='%s'", evt.TenantID)
				c.poolRegistry.Evict(evt.TenantID)
				c.routingRegistry.Delete(evt.TenantID)
				_ = d.Ack(false)
			}
		}
	}()

	return nil
}
