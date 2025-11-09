package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/rabbitmq"

	amqp "github.com/rabbitmq/amqp091-go"
)

type TenantPublisher struct {
	client *rabbitmq.Client
}

func NewTenantPublisher(client *rabbitmq.Client) (*TenantPublisher, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange for tenant publisher: %w", err)
	}
	return &TenantPublisher{client: client}, nil
}

func (p *TenantPublisher) PublishWorkspaceInitiated(ctx context.Context, evt domain.WorkspaceInitiatedEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal WorkspaceInitiated event: %w", err)
	}
	err = p.client.Channel.PublishWithContext(
		ctx,
		domain.ExchangeCompanyEvents,
		domain.RoutingKeyWorkspaceInitiated,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish WorkspaceInitiated event: %w", err)
	}
	log.Printf("TenantPublisher: Published WorkspaceInitiated for tenant_id='%s'", evt.TenantID)
	return nil
}

func (p *TenantPublisher) PublishWorkspaceReady(ctx context.Context, evt domain.WorkspaceReadyEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal WorkspaceReady event: %w", err)
	}
	err = p.client.Channel.PublishWithContext(
		ctx,
		domain.ExchangeCompanyEvents,
		domain.RoutingKeyWorkspaceReady,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish WorkspaceReady event: %w", err)
	}
	log.Printf("TenantPublisher: Published WorkspaceReady for tenant_id='%s'", evt.TenantID)
	return nil
}
