package main

import (
	"context"
	"fmt"

	"notification-service/internal/consumer"
	"notification-service/internal/infrastructure/postgres"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
)

// consumerRunner manages and launches all inbound queue consumers for Notification Service.
type consumerRunner struct {
	tenantProvisionedConsumer *consumer.TenantProvisionedConsumer
}

// registerConsumers initializes all inbound RabbitMQ event queue consumers.
func registerConsumers(dbClient *postgres.Client, rmqClient *rabbitmq.Client, notifService service.NotificationService) (*consumerRunner, error) {
	tenantProvisionedConsumer, err := consumer.NewTenantProvisionedConsumer(dbClient.DB, rmqClient, notifService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize TenantProvisionedConsumer: %w", err)
	}

	return &consumerRunner{
		tenantProvisionedConsumer: tenantProvisionedConsumer,
	}, nil
}

// start launches listener loops for all registered consumers.
func (cr *consumerRunner) start(ctx context.Context) error {
	if err := cr.tenantProvisionedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start TenantProvisionedConsumer: %w", err)
	}
	return nil
}
