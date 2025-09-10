package main

import (
	"context"
	"fmt"

	"tenant-service/internal/consumer"
	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/service"
)

// consumerRunner manages and launches all inbound queue consumers.
type consumerRunner struct {
	userConsumer *consumer.UserRegisteredConsumer
}

// registerConsumers initializes all inbound RabbitMQ event queue consumers.
func registerConsumers(dbClient *postgres.Client, rmqClient *rabbitmq.Client, provisionerService service.ProvisionerService) (*consumerRunner, error) {
	userConsumer, err := consumer.NewUserRegisteredConsumer(dbClient.DB, rmqClient, provisionerService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize UserRegisteredConsumer: %w", err)
	}

	return &consumerRunner{
		userConsumer: userConsumer,
	}, nil
}

// start launches listener loops for all registered consumers.
func (cr *consumerRunner) start(ctx context.Context) error {
	if err := cr.userConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start UserRegisteredConsumer: %w", err)
	}
	return nil
}
