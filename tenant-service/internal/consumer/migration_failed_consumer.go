package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/rabbitmq"
)

type MigrationFailedConsumerParams struct {
	TxManager                TxManager
	Client                   *rabbitmq.Client
	InboxService             InboxService
	MigrationRollbackService MigrationRollbackService
}

// MigrationRollbackService is the consumer-side interface expected by
// MigrationFailedConsumer. It exposes the business operation that resets a
// tenant to ACTIVE and stages the unfreeze broadcast; the repository work is
// owned by the Layer-2 service.
type MigrationRollbackService interface {
	RollbackFailedMigration(ctx context.Context, tenantID string) error
}

type MigrationFailedConsumer struct {
	txManager       TxManager
	client          *rabbitmq.Client
	inboxService    InboxService
	rollbackService MigrationRollbackService
}

func NewMigrationFailedConsumer(params MigrationFailedConsumerParams) (*MigrationFailedConsumer, error) {
	if params.InboxService == nil {
		return nil, errors.New("inboxService is required")
	}
	if params.MigrationRollbackService == nil {
		return nil, errors.New("migrationRollbackService is required")
	}

	consumer := &MigrationFailedConsumer{
		txManager:       params.TxManager,
		client:          params.Client,
		inboxService:    params.InboxService,
		rollbackService: params.MigrationRollbackService,
	}

	if err := consumer.setupTopology(); err != nil {
		return nil, err
	}

	return consumer, nil
}

func (c *MigrationFailedConsumer) setupTopology() error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := c.client.DeclareAndBindQueue(domain.QueueTenantServiceMigrationFailed, domain.ExchangeCompanyEvents, domain.RoutingKeyTenantMigrationFailed); err != nil {
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	return nil
}

func (c *MigrationFailedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := c.client.ConnContext()

			err := c.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("MigrationFailedConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := c.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("MigrationFailedConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (c *MigrationFailedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := c.setupTopology(); err != nil {
		return err
	}

	if c.client == nil || c.client.Channel == nil {
		return errors.New("channel is nil")
	}

	msgs, err := c.client.Channel.Consume(
		domain.QueueTenantServiceMigrationFailed,
		"tenant-service-migration-failed-consumer",
		false, false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("TenantService listening for '%s' events on queue '%s'...", domain.RoutingKeyTenantMigrationFailed, domain.QueueTenantServiceMigrationFailed)

	for {
		select {
		case <-appCtx.Done():
			log.Printf("MigrationFailedConsumer: Context cancelled, shutting down.")
			return appCtx.Err()

		case <-connCtx.Done():
			return connCtx.Err()

		case d, ok := <-msgs:
			if !ok {
				return errors.New("delivery channel closed")
			}

			_ = c.handleDelivery(appCtx, d)
		}
	}
}

func (c *MigrationFailedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	var evt domain.TenantMigrationFailedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("MigrationFailedConsumer Error: Bad payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	log.Printf("MigrationFailedConsumer: Rollback triggered for tenant='%s' event_id='%s' reason='%s'",
		evt.TenantID, evt.EventID, evt.Reason)

	err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		isDup, err := c.inboxService.ClaimEvent(txCtx, evt.EventID)
		if err != nil {
			return fmt.Errorf("failed to claim inbox event: %w", err)
		}
		if isDup {
			log.Printf("MigrationFailedConsumer: Duplicate event_id='%s' for tenant='%s', skipping.", evt.EventID, evt.TenantID)
			return nil
		}

		// Reset tenant status back to ACTIVE and stage the unfreeze broadcast
		// (both owned by the Layer-2 service within the outer Unit-of-Work).
		if err := c.rollbackService.RollbackFailedMigration(txCtx, evt.TenantID); err != nil {
			return err
		}

		log.Printf("MigrationFailedConsumer: Successfully reset status to ACTIVE & staged InfraChanged for tenant='%s'", evt.TenantID)
		return nil
	})

	if err != nil {
		log.Printf("MigrationFailedConsumer Error: Rollback saga failed for tenant='%s': %v", evt.TenantID, err)
		_ = d.Nack(false, true)
		return err
	}

	_ = d.Ack(false)
	return nil
}
