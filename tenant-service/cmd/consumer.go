package main

import (
	"context"
	"fmt"

	"tenant-service/internal/consumer"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/service"
	"tenant-service/internal/txcontext"
)

type consumerRunner struct {
	tenantOrderDBReadyConsumer *consumer.TenantOrderDBReadyConsumer
}

func registerConsumers(txManager txcontext.TxManager, rmqClient *rabbitmq.Client, workspaceSvc *service.WorkspaceService) (*consumerRunner, error) {
	c, err := consumer.NewTenantOrderDBReadyConsumer(txManager, rmqClient, workspaceSvc)
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
