package publisher

import (
	"context"
	"fmt"
	"log"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/rabbitmq"
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
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceInitiated, evt); err != nil {
		return fmt.Errorf("tenant_publisher: failed to publish WorkspaceInitiated event: %w", err)
	}
	log.Printf("TenantPublisher: Published WorkspaceInitiated event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}

func (p *TenantPublisher) PublishWorkspaceReady(ctx context.Context, evt domain.WorkspaceReadyEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceReady, evt); err != nil {
		return fmt.Errorf("tenant_publisher: failed to publish WorkspaceReady event: %w", err)
	}
	log.Printf("TenantPublisher: Published WorkspaceReady event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}

func (p *TenantPublisher) PublishInfrastructureLocking(ctx context.Context, evt domain.InfrastructureLockingEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyInfrastructureLocking, evt); err != nil {
		return fmt.Errorf("tenant_publisher: failed to publish InfrastructureLocking event: %w", err)
	}
	log.Printf("TenantPublisher: Published InfrastructureLocking event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}

func (p *TenantPublisher) PublishInfraChanged(ctx context.Context, evt domain.InfraChangedEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyInfraChanged, evt); err != nil {
		return fmt.Errorf("tenant_publisher: failed to publish InfraChanged event: %w", err)
	}
	log.Printf("TenantPublisher: Published InfraChanged event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}

func (p *TenantPublisher) PublishMigrationFailed(ctx context.Context, evt domain.TenantMigrationFailedEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyTenantMigrationFailed, evt); err != nil {
		return fmt.Errorf("tenant_publisher: failed to publish TenantMigrationFailed event: %w", err)
	}
	log.Printf("TenantPublisher: Published TenantMigrationFailed event_id='%s' tenant_id='%s'", evt.EventID, evt.TenantID)
	return nil
}

