package main

import (
	"context"
	"fmt"

	"order-service/internal/consumer"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/publisher"
	"order-service/internal/registry"
	"order-service/internal/service"
)

// consumerRunner manages all inbound queue consumers for order-service.
type consumerRunner struct {
	infraProvisionedConsumer      *consumer.InfrastructureProvisionedConsumer
	infrastructureChangedConsumer *consumer.InfrastructureChangedConsumer
}

func registerConsumers(
	rmqClient *rabbitmq.Client,
	migrationSvc *service.MigrationService,
	poolReg *registry.PoolRegistry,
	routingReg *registry.RoutingRegistry,
	sharedSecret string,
	sharedDBPass string,
) (*consumerRunner, error) {
	orderDBReadyPub, err := publisher.NewOrderDBReadyPublisher(rmqClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize OrderDBReadyPublisher: %w", err)
	}

	ipConsumer, err := consumer.NewInfrastructureProvisionedConsumer(consumer.InfrastructureProvisionedConsumerParams{
		Client:           rmqClient,
		Publisher:        orderDBReadyPub,
		MigrationService: migrationSvc,
		PoolRegistry:     poolReg,
		RoutingRegistry:  routingReg,
		SharedSecret:     sharedSecret,
		SharedDBPass:     sharedDBPass,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize InfrastructureProvisionedConsumer: %w", err)
	}

	icConsumer, err := consumer.NewInfrastructureChangedConsumer(rmqClient, poolReg, routingReg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize InfrastructureChangedConsumer: %w", err)
	}

	return &consumerRunner{
		infraProvisionedConsumer:      ipConsumer,
		infrastructureChangedConsumer: icConsumer,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if err := cr.infraProvisionedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start InfrastructureProvisionedConsumer: %w", err)
	}
	if err := cr.infrastructureChangedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start InfrastructureChangedConsumer: %w", err)
	}
	return nil
}
