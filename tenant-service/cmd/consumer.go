package main

import (
	"context"
	"fmt"

	"tenant-service/internal/consumer"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/service"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type consumerRunner struct {
	tenantOrderDBReadyConsumer *consumer.TenantOrderDBReadyConsumer
	migrationFailedConsumer    *consumer.MigrationFailedConsumer
}

func registerConsumers(
	txManager *txcontext.SQLTxManager,
	rmqClient *rabbitmq.Client,
	tenantInfrastructureService *service.TenantInfrastructureService,
	inboxService *service.InboxService,
	workspaceService *service.WorkspaceService,
) (*consumerRunner, error) {
	c := consumer.NewTenantOrderDBReadyConsumer(consumer.TenantOrderDBReadyConsumerParams{
		TxManager:                   txManager,
		Client:                      rmqClient,
		TenantInfrastructureService: tenantInfrastructureService,
		InboxService:                inboxService,
	})

	mfConsumer := consumer.NewMigrationFailedConsumer(consumer.MigrationFailedConsumerParams{
		TxManager:                txManager,
		Client:                   rmqClient,
		InboxService:             inboxService,
		MigrationRollbackService: workspaceService,
	})

	return &consumerRunner{
		tenantOrderDBReadyConsumer: c,
		migrationFailedConsumer:    mfConsumer,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if cr.tenantOrderDBReadyConsumer != nil {
		if err := cr.tenantOrderDBReadyConsumer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start TenantOrderDBReadyConsumer: %w", err)
		}
	}
	if cr.migrationFailedConsumer != nil {
		if err := cr.migrationFailedConsumer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start MigrationFailedConsumer: %w", err)
		}
	}
	return nil
}
