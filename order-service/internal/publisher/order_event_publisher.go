package publisher

import (
	"context"
	"fmt"
	"log"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
)

// OrderEventPublisher publishes order domain events to the company.events exchange.
type OrderEventPublisher struct {
	client *rabbitmq.Client
}

func NewOrderEventPublisher(client *rabbitmq.Client) (*OrderEventPublisher, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange for order event publisher: %w", err)
	}
	return &OrderEventPublisher{client: client}, nil
}

// PublishOrderCreated publishes an order.created event to the company.events exchange.
func (p *OrderEventPublisher) PublishOrderCreated(ctx context.Context, evt domain.OrderCreatedEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyOrderCreated, evt); err != nil {
		return fmt.Errorf("order_event_publisher: failed to publish OrderCreated event: %w", err)
	}
	log.Printf("OrderEventPublisher: Published OrderCreated event_id='%s' tenant_id='%s' order_id='%s'",
		evt.EventID, evt.TenantID, evt.OrderID)
	return nil
}
