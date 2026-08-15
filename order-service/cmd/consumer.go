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
	infrastructureLockingConsumer *consumer.InfrastructureLockingConsumer
}

func registerConsumers(
	rmqClient *rabbitmq.Client,
	migrationService *service.MigrationService,
	poolReg *registry.PoolRegistry,
	routingReg *registry.RoutingRegistry,
	sharedSecret string,
	sharedDBPass string,
) (*consumerRunner, error) {
	orderDBReadyPub, err := publisher.NewOrderDBReadyPublisher(rmqClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize OrderDBReadyPublisher: %w", err)
	}

	ipConsumer := consumer.NewInfrastructureProvisionedConsumer(consumer.InfrastructureProvisionedConsumerParams{
		Client:           rmqClient,
		Publisher:        orderDBReadyPub,
		MigrationService: migrationService,
		PoolRegistry:     poolReg,
		RoutingRegistry:  routingReg,
		SharedSecret:     sharedSecret,
		SharedDBPass:     sharedDBPass,
	})

	icConsumer := consumer.NewInfrastructureChangedConsumer(consumer.InfrastructureChangedConsumerParams{
		Client:          rmqClient,
		PoolRegistry:    poolReg,
		RoutingRegistry: routingReg,
	})

	ilConsumer := consumer.NewInfrastructureLockingConsumer(consumer.InfrastructureLockingConsumerParams{
		Client:          rmqClient,
		RoutingRegistry: routingReg,
	})

	return &consumerRunner{
		infraProvisionedConsumer:      ipConsumer,
		infrastructureChangedConsumer: icConsumer,
		infrastructureLockingConsumer: ilConsumer,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if err := cr.infraProvisionedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start InfrastructureProvisionedConsumer: %w", err)
	}
	if err := cr.infrastructureChangedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start InfrastructureChangedConsumer: %w", err)
	}
	if err := cr.infrastructureLockingConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start InfrastructureLockingConsumer: %w", err)
	}
	return nil
}
