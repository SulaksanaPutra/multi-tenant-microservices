package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"infra-provisioner/internal/domain"
	"infra-provisioner/internal/infrastructure/rabbitmq"

	amqp "github.com/rabbitmq/amqp091-go"
)

type InfrastructurePublisher struct {
	client *rabbitmq.Client
}

func NewInfrastructurePublisher(client *rabbitmq.Client) (*InfrastructurePublisher, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}
	return &InfrastructurePublisher{client: client}, nil
}

func (p *InfrastructurePublisher) PublishInfrastructureProvisioned(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error {
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("infrastructure_publisher: failed to marshal payload: %w", err)
	}

	err = p.client.Channel.PublishWithContext(
		ctx,
		domain.ExchangeCompanyEvents,
		domain.RoutingKeyInfrastructureProvisioned,
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         payload,
		},
	)
	if err != nil {
		return fmt.Errorf("infrastructure_publisher: failed to publish infrastructure.provisioned event: %w", err)
	}

	log.Printf("InfrastructurePublisher: Published infrastructure.provisioned event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}
