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
	ExchangeCompanyEvents        = "company.events"
	RoutingKeyWorkspaceInitiated = "workspace.initiated"
	RoutingKeyWorkspaceReady     = "workspace.ready"
)

// WorkspaceInitiatedEvent is published when a new tenant workspace is registered.
// EventID serves as the outbox row ID for consumer Inbox deduplication.
type WorkspaceInitiatedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Plan       string `json:"plan"`
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
}

// WorkspaceReadyEvent is published once all required domain services have checked in.
type WorkspaceReadyEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	OwnerEmail string `json:"owner_email"`
}

type TenantEventPublisher interface {
	PublishWorkspaceInitiated(ctx context.Context, evt WorkspaceInitiatedEvent) error
	PublishWorkspaceReady(ctx context.Context, evt WorkspaceReadyEvent) error
}

type RabbitMQTenantPublisher struct {
	client *rabbitmq.Client
}

func NewTenantPublisher(client *rabbitmq.Client) (*RabbitMQTenantPublisher, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange for tenant publisher: %w", err)
	}
	return &RabbitMQTenantPublisher{client: client}, nil
}

func (p *RabbitMQTenantPublisher) PublishWorkspaceInitiated(ctx context.Context, evt WorkspaceInitiatedEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal WorkspaceInitiated event: %w", err)
	}
	err = p.client.Channel.PublishWithContext(
		ctx,
		ExchangeCompanyEvents,
		RoutingKeyWorkspaceInitiated,
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
	log.Printf("TenantPublisher: Published WorkspaceInitiated for tenant_id='%s' plan='%s'", evt.TenantID, evt.Plan)
	return nil
}

func (p *RabbitMQTenantPublisher) PublishWorkspaceReady(ctx context.Context, evt WorkspaceReadyEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal WorkspaceReady event: %w", err)
	}
	err = p.client.Channel.PublishWithContext(
		ctx,
		ExchangeCompanyEvents,
		RoutingKeyWorkspaceReady,
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
