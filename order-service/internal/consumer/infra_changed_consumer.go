package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
)

const (
	RoutingKeyInfraChanged            = "tenant.infrastructure_changed"
	QueueOrderServiceInfraChanged     = "order_service_infrastructure_changed"
)

// InfraChangedEvent is published by tenant-service when a tenant upgrades their plan.
// order-service listens and evicts the stale cache entry so the next request
// fetches the fresh Dedicated DSN.
type InfraChangedEvent struct {
	EventID  string `json:"event_id"`
	TenantID string `json:"tenant_id"`
}

// InfraChangedConsumer handles cache invalidation and materialized view updates when a tenant's infrastructure changes.
type InfraChangedConsumer struct {
	client          *rabbitmq.Client
	poolRegistry    *registry.PoolRegistry
	routingRegistry *registry.RoutingRegistry
}

func NewInfraChangedConsumer(client *rabbitmq.Client, poolReg *registry.PoolRegistry, routingReg *registry.RoutingRegistry) (*InfraChangedConsumer, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}
	if err := client.DeclareAndBindQueue(
		QueueOrderServiceInfraChanged, ExchangeCompanyEvents, RoutingKeyInfraChanged,
	); err != nil {
		return nil, fmt.Errorf("failed to bind infra-changed queue: %w", err)
	}
	return &InfraChangedConsumer{client: client, poolRegistry: poolReg, routingRegistry: routingReg}, nil
}

func (c *InfraChangedConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		QueueOrderServiceInfraChanged,
		"order-service-cache-invalidator",
		false, false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue '%s': %w", QueueOrderServiceInfraChanged, err)
	}

	log.Printf("OrderService: Listening for tenant.infrastructure_changed events...")

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case d, ok := <-msgs:
				if !ok {
					return
				}

				var evt InfraChangedEvent
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
