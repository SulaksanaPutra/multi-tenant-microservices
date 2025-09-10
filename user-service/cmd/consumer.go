package main

import (
	"context"
	"fmt"

	"user-service/internal/consumer"
	"user-service/internal/infrastructure/postgres"
	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/service"
)

type consumerRunner struct {
	tenantProvisionedConsumer *consumer.TenantProvisionedConsumer
}

func registerConsumers(dbClient *postgres.Client, rmqClient *rabbitmq.Client, userService service.UserService) (*consumerRunner, error) {
	tenantProvisionedConsumer, err := consumer.NewTenantProvisionedConsumer(consumer.TenantProvisionedConsumerParams{
		DB:          dbClient.DB,
		Client:      rmqClient,
		UserService: userService,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize TenantProvisionedConsumer: %w", err)
	}

	return &consumerRunner{
		tenantProvisionedConsumer: tenantProvisionedConsumer,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if err := cr.tenantProvisionedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start TenantProvisionedConsumer: %w", err)
	}
	return nil
}
