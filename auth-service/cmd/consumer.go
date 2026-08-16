package main

import (
	"context"
	"fmt"

	"auth-service/internal/consumer"
	"auth-service/internal/infrastructure/rabbitmq"
	"auth-service/internal/service"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type consumerRunner struct {
	userCreatedConsumer *consumer.UserCreatedConsumer
}

func registerConsumers(
	txManager *txcontext.SQLTxManager,
	rmqClient *rabbitmq.Client,
	inboxService *service.InboxService,
	membershipService *service.MembershipService,
) (*consumerRunner, error) {
	c := consumer.NewUserCreatedConsumer(consumer.UserCreatedConsumerParams{
		TxManager:         txManager,
		Client:            rmqClient,
		InboxService:      inboxService,
		MembershipService: membershipService,
	})

	return &consumerRunner{
		userCreatedConsumer: c,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if cr.userCreatedConsumer != nil {
		if err := cr.userCreatedConsumer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start UserCreatedConsumer: %w", err)
		}
	}
	return nil
}
