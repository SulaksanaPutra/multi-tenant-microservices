package main

import (
	"context"
	"fmt"

	"user-service/internal/consumer"
	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/service"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type consumerRunner struct {
	workspaceInitiatedConsumer *consumer.WorkspaceInitiatedConsumer
}

func registerConsumers(txManager *txcontext.SQLTxManager, rmqClient *rabbitmq.Client, inboxService *service.InboxService, userService *service.UserService) (*consumerRunner, error) {
	wiConsumer := consumer.NewWorkspaceInitiatedConsumer(consumer.WorkspaceInitiatedConsumerParams{
		TxManager:    txManager,
		Client:       rmqClient,
		InboxService: inboxService,
		UserService:  userService,
	})

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
