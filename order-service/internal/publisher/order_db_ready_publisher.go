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
		return nil, fmt.Errorf("failed to declare exchange for order db ready publisher: %w", err)
	}
	return &OrderDBReadyPublisher{client: client}, nil
}

func (p *OrderDBReadyPublisher) PublishTenantOrderDBReady(ctx context.Context, evt domain.TenantOrderDBReadyEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyTenantOrderDBReady, evt); err != nil {
		return fmt.Errorf("order_db_ready_publisher: failed to publish TenantOrderDBReady event: %w", err)
	}

	log.Printf("OrderDBReadyPublisher: Published TenantOrderDBReady event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}

