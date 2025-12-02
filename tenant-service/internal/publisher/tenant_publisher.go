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
		return fmt.Errorf("failed to publish WorkspaceInitiated event: %w", err)
	}
	log.Printf("TenantPublisher: Published WorkspaceInitiated for tenant_id='%s'", evt.TenantID)
	return nil
}

func (p *TenantPublisher) PublishWorkspaceReady(ctx context.Context, evt domain.WorkspaceReadyEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceReady, evt); err != nil {
		return fmt.Errorf("failed to publish WorkspaceReady event: %w", err)
	}
	log.Printf("TenantPublisher: Published WorkspaceReady for tenant_id='%s'", evt.TenantID)
	return nil
}
