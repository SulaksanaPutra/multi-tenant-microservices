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
	infraProvisionedConsumer *consumer.InfrastructureProvisionedConsumer
	infraChangedConsumer     *consumer.InfraChangedConsumer
}

func registerConsumers(
	rmqClient *rabbitmq.Client,
	migrationSvc service.MigrationService,
	poolReg *registry.PoolRegistry,
	routingReg *registry.RoutingRegistry,
	sharedSecret string,
	sharedDBPass string,
) (*consumerRunner, error) {
	ipConsumer, err := consumer.NewInfrastructureProvisionedConsumer(consumer.InfrastructureProvisionedConsumerParams{
		Client:           rmqClient,
		MigrationService: migrationSvc,
		PoolRegistry:     poolReg,
		RoutingRegistry:  routingReg,
		SharedSecret:     sharedSecret,
		SharedDBPass:     sharedDBPass,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize InfrastructureProvisionedConsumer: %w", err)
	}

	icConsumer, err := consumer.NewInfraChangedConsumer(rmqClient, poolReg, routingReg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize InfraChangedConsumer: %w", err)
	}

	return &consumerRunner{
		infraProvisionedConsumer: ipConsumer,
		infraChangedConsumer:     icConsumer,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if err := cr.infraProvisionedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start InfrastructureProvisionedConsumer: %w", err)
	}
	if err := cr.infraChangedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start InfraChangedConsumer: %w", err)
	}
	return nil
}
