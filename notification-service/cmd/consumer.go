package main

import (
	"context"
	"fmt"

	"notification-service/internal/consumer"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
	"notification-service/internal/txcontext"
)

type consumerRunner struct {
	workspaceReadyConsumer *consumer.WorkspaceReadyConsumer
	userCreatedConsumer    *consumer.UserCreatedConsumer
}

func registerConsumers(txManager *txcontext.SQLTxManager, rmqClient *rabbitmq.Client, notifService *service.NotificationService) (*consumerRunner, error) {
	workspaceReadyConsumer, err := consumer.NewWorkspaceReadyConsumer(txManager, rmqClient, notifService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize WorkspaceReadyConsumer: %w", err)
	}

	userCreatedConsumer, err := consumer.NewUserCreatedConsumer(txManager, rmqClient, notifService)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize UserCreatedConsumer: %w", err)
	}

	return &consumerRunner{
		workspaceReadyConsumer: workspaceReadyConsumer,
		userCreatedConsumer:    userCreatedConsumer,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if err := cr.workspaceReadyConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start WorkspaceReadyConsumer: %w", err)
	}
	if err := cr.userCreatedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start UserCreatedConsumer: %w", err)
	}
	return nil
}
