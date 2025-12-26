package main

import (
	"context"
	"fmt"

	"tenant-service/internal/consumer"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/repository"
	"tenant-service/internal/service"
	"tenant-service/internal/txcontext"
)

type consumerRunner struct {
	tenantOrderDBReadyConsumer *consumer.TenantOrderDBReadyConsumer
}

func registerConsumers(txManager *txcontext.SQLTxManager, rmqClient *rabbitmq.Client, tenantInfrastructureSvc *service.TenantInfrastructureService, inboxRepo *repository.InboxRepository) (*consumerRunner, error) {
	c, err := consumer.NewTenantOrderDBReadyConsumer(consumer.TenantOrderDBReadyConsumerParams{
		TxManager:                   txManager,
		Client:                      rmqClient,
		TenantInfrastructureService: tenantInfrastructureSvc,
		InboxRepo:                   inboxRepo,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to register TenantOrderDBReadyConsumer: %w", err)
	}

	return &consumerRunner{
		tenantOrderDBReadyConsumer: c,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if cr.tenantOrderDBReadyConsumer != nil {
		if err := cr.tenantOrderDBReadyConsumer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start TenantOrderDBReadyConsumer: %w", err)
		}
	}
	return nil
}
