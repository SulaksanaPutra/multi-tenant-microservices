package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
	"tenant-service/internal/infrastructure/rabbitmq"
)

const (
	ExchangeCompanyEvents       = "company.events"
	RoutingKeyTenantProvisioned = "tenant.provisioned"
)

type TenantProvisionedEvent struct {
	TenantID   string `json:"tenant_id"`
	TenantSlug string `json:"tenant_slug"`
	UserID     string `json:"user_id"`
}

type TenantEventPublisher interface {
	PublishTenantProvisioned(ctx context.Context, evt TenantProvisionedEvent) error
}

type RabbitMQTenantPublisher struct {
	client *rabbitmq.Client
}

func NewTenantPublisher(client *rabbitmq.Client) (*RabbitMQTenantPublisher, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange for tenant publisher: %w", err)
	}

	return &RabbitMQTenantPublisher{
		client: client,
	}, nil
}

func (p *RabbitMQTenantPublisher) PublishTenantProvisioned(ctx context.Context, evt TenantProvisionedEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal TenantProvisioned event: %w", err)
	}

	err = p.client.Channel.PublishWithContext(
		ctx,
		ExchangeCompanyEvents,       // exchange
		RoutingKeyTenantProvisioned, // routing key
		false,                       // mandatory
		false,                       // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish TenantProvisioned event: %w", err)
	}

	log.Printf("TenantPublisher: Published TenantProvisioned event for tenant_id='%s' (user_id='%s')", evt.TenantID, evt.UserID)
	return nil
}
