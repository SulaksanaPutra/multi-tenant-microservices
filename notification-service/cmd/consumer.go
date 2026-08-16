package main

import (
	"context"
	"fmt"

	"notification-service/internal/consumer"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type consumerRunner struct {
	workspaceReadyConsumer *consumer.WorkspaceReadyConsumer
	userCreatedConsumer    *consumer.UserCreatedConsumer
	orderCreatedConsumer   *consumer.OrderCreatedConsumer
}

func registerConsumers(
	txManager *txcontext.SQLTxManager,
	rmqClient *rabbitmq.Client,
	inboxService *service.InboxService,
	notificationService *service.NotificationService,
	authClient consumer.AuthClient,
	mailer consumer.Mailer,
) (*consumerRunner, error) {
	workspaceReadyConsumer := consumer.NewWorkspaceReadyConsumer(consumer.WorkspaceReadyConsumerParams{
		TxManager:           txManager,
		Client:              rmqClient,
		InboxService:        inboxService,
		NotificationService: notificationService,
		AuthClient:          authClient,
		Mailer:              mailer,
	})

	userCreatedConsumer := consumer.NewUserCreatedConsumer(consumer.UserCreatedConsumerParams{
		TxManager:           txManager,
		Client:              rmqClient,
		InboxService:        inboxService,
		NotificationService: notificationService,
		AuthClient:          authClient,
		Mailer:              mailer,
	})

	orderCreatedConsumer := consumer.NewOrderCreatedConsumer(consumer.OrderCreatedConsumerParams{
		TxManager:           txManager,
		Client:              rmqClient,
		InboxService:        inboxService,
		NotificationService: notificationService,
	})

	return &consumerRunner{
		workspaceReadyConsumer: workspaceReadyConsumer,
		userCreatedConsumer:    userCreatedConsumer,
		orderCreatedConsumer:   orderCreatedConsumer,
	}, nil
}

func (consumerRunner *consumerRunner) start(ctx context.Context) error {
	if err := consumerRunner.workspaceReadyConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start WorkspaceReadyConsumer: %w", err)
	}
	if err := consumerRunner.userCreatedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start UserCreatedConsumer: %w", err)
	}
	if err := consumerRunner.orderCreatedConsumer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start OrderCreatedConsumer: %w", err)
	}
	return nil
}
