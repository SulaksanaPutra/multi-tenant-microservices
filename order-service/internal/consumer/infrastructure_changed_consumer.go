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

	// Declare an exclusive, auto-delete anonymous queue for this specific replica process
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

func (c *InfrastructureChangedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

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
				log.Printf("InfrastructureChangedConsumer: Failed to start consume on '%s': %v. Waiting for reconnect...", c.queueName, err)
			} else {
				log.Printf("OrderService: Listening for tenant.infrastructure_changed broadcast events on exclusive queue '%s'...", c.queueName)

				consumedAll := false
				for !consumedAll {
					select {
					case <-ctx.Done():
						return
					case d, ok := <-msgs:
						if !ok {
							log.Printf("InfrastructureChangedConsumer: Delivery channel closed for queue '%s'. Triggering recovery...", c.queueName)
							consumedAll = true
							break
						}

						var evt domain.InfraChangedEvent
						if err := json.Unmarshal(d.Body, &evt); err != nil {
							log.Printf("InfrastructureChangedConsumer Error: Bad payload: %v", err)
							_ = d.Nack(false, false)
							continue
						}

						log.Printf("InfrastructureChangedConsumer: Evicting pool & resetting routing metadata for tenant='%s'", evt.TenantID)
						c.poolRegistry.Evict(evt.TenantID)
						c.routingRegistry.Delete(evt.TenantID)
						_ = d.Ack(false)
					}
				}
			}

			// Connection flap / socket drop recovery phase:
			select {
			case <-ctx.Done():
				return
			case <-c.client.NotifyReconnect():
				log.Println("InfrastructureChangedConsumer: Reconnect signal received! Purging all local caches...")
				c.poolRegistry.PurgeAll()
				c.routingRegistry.PurgeAll()

				qName, err := c.setupTopology()
				if err != nil {
					log.Printf("InfrastructureChangedConsumer: Failed to setup topology after reconnect: %v", err)
					continue
				}
				c.queueName = qName
				log.Printf("InfrastructureChangedConsumer: Successfully re-bound exclusive queue to '%s'", c.queueName)
			}
		}
	}()

	return nil
}
