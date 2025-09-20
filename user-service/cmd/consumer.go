package main

import (
	"context"
	"fmt"

	"user-service/internal/consumer"
	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/service"
	"user-service/internal/txctx"
)

type consumerRunner struct {
	workspaceInitiatedConsumer *consumer.WorkspaceInitiatedConsumer
}

func registerConsumers(txManager txctx.TxManager, rmqClient *rabbitmq.Client, userService service.UserService) (*consumerRunner, error) {
	wiConsumer, err := consumer.NewWorkspaceInitiatedConsumer(txManager, rmqClient, userService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize WorkspaceInitiatedConsumer: %w", err)
	}

	return &consumerRunner{
		workspaceInitiatedConsumer: wiConsumer,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if err := cr.workspaceInitiatedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start WorkspaceInitiatedConsumer: %w", err)
	}
	return nil
}
