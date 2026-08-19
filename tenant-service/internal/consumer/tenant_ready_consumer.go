package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/service"
)

type TenantOrderDBReadyConsumerParams struct {
	TxManager                   TxManager
	Client                      AMQPClient
	TenantInfrastructureService TenantInfrastructureService
	InboxService                InboxService
}

type TenantOrderDBReadyConsumer struct {
	txManager                   TxManager
	client                      AMQPClient
	tenantInfrastructureService TenantInfrastructureService
	inboxService                InboxService
}

func NewTenantOrderDBReadyConsumer(params TenantOrderDBReadyConsumerParams) *TenantOrderDBReadyConsumer {
	return &TenantOrderDBReadyConsumer{
		txManager:                   params.TxManager,
		client:                      params.Client,
		tenantInfrastructureService: params.TenantInfrastructureService,
		inboxService:                params.InboxService,
	}
}

func (tenantOrderDBReadyConsumer *TenantOrderDBReadyConsumer) setupTopology() error {
	if err := tenantOrderDBReadyConsumer.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}

	if err := tenantOrderDBReadyConsumer.client.DeclareAndBindQueue(domain.QueueTenantServiceOrderReady, domain.ExchangeCompanyEvents, domain.RoutingKeyTenantOrderDBReady); err != nil {
		return fmt.Errorf("failed to bind queue '%s': %w", domain.QueueTenantServiceOrderReady, err)
	}

	return nil
}

func (tenantOrderDBReadyConsumer *TenantOrderDBReadyConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := tenantOrderDBReadyConsumer.client.ConnContext()

			err := tenantOrderDBReadyConsumer.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("TenantOrderDBReadyConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := tenantOrderDBReadyConsumer.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("TenantOrderDBReadyConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (tenantOrderDBReadyConsumer *TenantOrderDBReadyConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := tenantOrderDBReadyConsumer.setupTopology(); err != nil {
		return err
	}

	msgs, err := tenantOrderDBReadyConsumer.client.Consume(
		domain.QueueTenantServiceOrderReady,
		"tenant-service-ready-consumer",
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("TenantService: Listening for '%s' events on queue '%s'...", domain.RoutingKeyTenantOrderDBReady, domain.QueueTenantServiceOrderReady)

	for {
		select {
		case <-appCtx.Done():
			log.Printf("TenantOrderDBReadyConsumer: Context cancelled, shutting down.")
			return appCtx.Err()

		case <-connCtx.Done():
			return connCtx.Err()

		case d, ok := <-msgs:
			if !ok {
				return errors.New("delivery channel closed")
			}

			_ = tenantOrderDBReadyConsumer.handleDelivery(appCtx, d)
		}
	}
}

func (tenantOrderDBReadyConsumer *TenantOrderDBReadyConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	var evt domain.TenantOrderDBReadyEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("TenantOrderDBReadyConsumer Error: Bad payload: %v", err)
		_ = d.Nack(false, false)
		return err
	}

	if strings.TrimSpace(evt.TenantID) == "" {
		log.Printf("TenantOrderDBReadyConsumer Error: Missing tenant_id in event payload, discarding message.")
		_ = d.Nack(false, false)
		return errors.New("missing tenant_id in payload")
	}

	deliveryCount := getDeliveryCount(d.Headers)
	if deliveryCount >= 3 {
		log.Printf("[DLQ] TenantOrderDBReadyConsumer: Max delivery count reached for event_id='%s' tenant_id='%s' (delivery_count=%d). Routing to DLQ.",
			evt.EventID, evt.TenantID, deliveryCount)
		_ = d.Nack(false, false)
		return errors.New("max delivery count reached")
	}

	log.Printf("TenantOrderDBReadyConsumer: Received order DB ready for tenant='%s' service='%s'",
		evt.TenantID, evt.ServiceName)

	err := tenantOrderDBReadyConsumer.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		isDuplicate, err := tenantOrderDBReadyConsumer.inboxService.ClaimEvent(txCtx, evt.EventID)
		if err != nil {
			return fmt.Errorf("failed to claim inbox event: %w", err)
		}
		if isDuplicate {
			log.Printf("TenantOrderDBReadyConsumer: Duplicate event_id='%s' detected for tenant='%s', skipping processing.", evt.EventID, evt.TenantID)
			return nil
		}

		input := service.InfrastructureUpdateInput{
			TenantID:    evt.TenantID,
			ServiceName: evt.ServiceName,
			DBHost:      evt.DBHost,
			DBPort:      evt.DBPort,
			DBName:      evt.DBName,
			DBUser:      evt.DBUser,
			SchemaName:  evt.SchemaName,
		}
		return tenantOrderDBReadyConsumer.tenantInfrastructureService.HandleInfrastructureUpdate(txCtx, input)
	})

	if err != nil {
		log.Printf("TenantOrderDBReadyConsumer Error: Failed to handle infra update for tenant='%s': %v", evt.TenantID, err)
		if errors.Is(err, domain.ErrTenantIDRequired) || errors.Is(err, domain.ErrServiceNameRequired) {
			_ = d.Nack(false, false)
		} else {
			_ = d.Nack(false, true)
		}
		return err
	}

	_ = d.Ack(false)
	log.Printf("TenantOrderDBReadyConsumer: Successfully processed order DB ready event for tenant='%s'", evt.TenantID)
	return nil
}
