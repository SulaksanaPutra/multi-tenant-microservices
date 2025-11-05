package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"

	amqp "github.com/rabbitmq/amqp091-go"
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
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("order_db_ready_publisher: failed to marshal payload: %w", err)
	}

	err = p.client.Channel.PublishWithContext(
		ctx,
		domain.ExchangeCompanyEvents,
		domain.RoutingKeyTenantOrderDBReady,
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         payload,
		},
	)
	if err != nil {
		return fmt.Errorf("order_db_ready_publisher: failed to publish tenant.order_db.ready event: %w", err)
	}

	log.Printf("OrderDBReadyPublisher: Published tenant.order_db.ready event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}
