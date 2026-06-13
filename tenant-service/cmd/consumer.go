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
	migrationFailedConsumer    *consumer.MigrationFailedConsumer
}

func registerConsumers(
	txManager *txcontext.SQLTxManager,
	rmqClient *rabbitmq.Client,
	tenantInfrastructureService *service.TenantInfrastructureService,
	inboxService *service.InboxService,
	tenantRepository consumer.TenantRepository,
	outboxRepository consumer.OutboxRepository,
) (*consumerRunner, error) {
	c, err := consumer.NewTenantOrderDBReadyConsumer(consumer.TenantOrderDBReadyConsumerParams{
		TxManager:                   txManager,
		Client:                      rmqClient,
		TenantInfrastructureService: tenantInfrastructureService,
		InboxService:                inboxService,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to register TenantOrderDBReadyConsumer: %w", err)
	}

	mfConsumer, err := consumer.NewMigrationFailedConsumer(consumer.MigrationFailedConsumerParams{
		TxManager:        txManager,
		Client:           rmqClient,
		InboxService:     inboxService,
		TenantRepository: tenantRepository,
		OutboxRepository: outboxRepository,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to register MigrationFailedConsumer: %w", err)
	}

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
