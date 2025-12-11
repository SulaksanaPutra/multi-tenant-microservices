package publisher

import (
	"context"
	"fmt"
	"log"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
)

type OrderDBReadyPublisher struct {
	client *rabbitmq.Client
}

func NewOrderDBReadyPublisher(client *rabbitmq.Client) (*OrderDBReadyPublisher, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}
	return &OrderDBReadyPublisher{client: client}, nil
}

func (p *OrderDBReadyPublisher) Publish(ctx context.Context, evt domain.TenantOrderDBReadyEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyTenantOrderDBReady, evt); err != nil {
		return fmt.Errorf("order_db_ready_publisher: failed to publish tenant.order_db.ready event: %w", err)
	}

	log.Printf("OrderDBReadyPublisher: Published tenant.order_db.ready event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}
