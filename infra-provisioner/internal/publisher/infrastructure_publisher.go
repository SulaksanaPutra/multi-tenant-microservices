package publisher

import (
	"context"
	"fmt"
	"log"

	"infra-provisioner/internal/domain"
	"infra-provisioner/internal/infrastructure/rabbitmq"
)

type InfrastructurePublisher struct {
	client *rabbitmq.Client
}

func NewInfrastructurePublisher(client *rabbitmq.Client) (*InfrastructurePublisher, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange for infrastructure publisher: %w", err)
	}
	return &InfrastructurePublisher{client: client}, nil
}

func (p *InfrastructurePublisher) PublishInfrastructureProvisioned(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyInfrastructureProvisioned, evt); err != nil {
		return fmt.Errorf("infrastructure_publisher: failed to publish InfrastructureProvisioned event: %w", err)
	}

	log.Printf("InfrastructurePublisher: Published InfrastructureProvisioned event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}

func (p *InfrastructurePublisher) PublishTenantMigrationFailed(ctx context.Context, evt domain.TenantMigrationFailedEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyTenantMigrationFailed, evt); err != nil {
		return fmt.Errorf("infrastructure_publisher: failed to publish TenantMigrationFailed event: %w", err)
	}

	log.Printf("InfrastructurePublisher: Published TenantMigrationFailed event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}


