package main

import (
	"context"
	"fmt"

	"order-service/internal/consumer"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
	"order-service/internal/service"
)

// consumerRunner manages all inbound queue consumers for order-service.
type consumerRunner struct {
	workspaceInitiatedConsumer *consumer.WorkspaceInitiatedConsumer
	infraChangedConsumer       *consumer.InfraChangedConsumer
}

func registerConsumers(
	rmqClient *rabbitmq.Client,
	provisioner service.ProvisionerService,
	reg *registry.PoolRegistry,
	tenantServiceURL string,
) (*consumerRunner, error) {
	wiConsumer, err := consumer.NewWorkspaceInitiatedConsumer(consumer.WorkspaceInitiatedConsumerParams{
		Client:           rmqClient,
		Provisioner:      provisioner,
		Registry:         reg,
		TenantServiceURL: tenantServiceURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize WorkspaceInitiatedConsumer: %w", err)
	}

	icConsumer, err := consumer.NewInfraChangedConsumer(rmqClient, reg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize InfraChangedConsumer: %w", err)
	}

	return &consumerRunner{
		workspaceInitiatedConsumer: wiConsumer,
		infraChangedConsumer:       icConsumer,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if err := cr.workspaceInitiatedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start WorkspaceInitiatedConsumer: %w", err)
	}
	if err := cr.infraChangedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start InfraChangedConsumer: %w", err)
	}
	return nil
}
