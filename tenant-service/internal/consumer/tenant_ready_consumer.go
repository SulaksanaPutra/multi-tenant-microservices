package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/service"
	"tenant-service/internal/txcontext"
)

const (
	ExchangeCompanyEvents        = "company.events"
	RoutingKeyTenantOrderDBReady = "tenant.order_db.ready"
	QueueTenantServiceOrderReady = "tenant_service_order_db_ready"
)

type TenantOrderDBReadyEvent struct {
	EventID     string `json:"event_id"`
	TenantID    string `json:"tenant_id"`
	ServiceName string `json:"service_name"`
	DBHost      string `json:"db_host"`
	DBPort      int    `json:"db_port"`
	DBName      string `json:"db_name"`
	DBUser      string `json:"db_user"`
	SchemaName  string `json:"schema_name"`
}

// WorkspaceService is the consumer-side interface expected by TenantOrderDBReadyConsumer.
type WorkspaceService interface {
	HandleInfrastructureUpdate(ctx context.Context, input service.InfraUpdateInput) error
}

type TenantOrderDBReadyConsumer struct {
	txManager        txcontext.TxManager
	client           *rabbitmq.Client
	workspaceService WorkspaceService
}

func NewTenantOrderDBReadyConsumer(txManager txcontext.TxManager, client *rabbitmq.Client, workspaceSvc WorkspaceService) (*TenantOrderDBReadyConsumer, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange '%s': %w", ExchangeCompanyEvents, err)
	}

	if err := client.DeclareAndBindQueue(QueueTenantServiceOrderReady, ExchangeCompanyEvents, RoutingKeyTenantOrderDBReady); err != nil {
		return nil, fmt.Errorf("failed to bind queue '%s': %w", QueueTenantServiceOrderReady, err)
	}

	return &TenantOrderDBReadyConsumer{
		txManager:        txManager,
		client:           client,
		workspaceService: workspaceSvc,
	}, nil
}

func (c *TenantOrderDBReadyConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		QueueTenantServiceOrderReady,
		"tenant-service-order-ready-consumer",
		false, // manual ack
		false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to consume queue '%s': %w", QueueTenantServiceOrderReady, err)
	}

	log.Printf("TenantService: Listening for '%s' events on queue '%s'...", RoutingKeyTenantOrderDBReady, QueueTenantServiceOrderReady)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("TenantOrderDBReadyConsumer: Context cancelled, shutting down.")
				return
			case d, ok := <-msgs:
				if !ok {
					log.Printf("TenantOrderDBReadyConsumer: Message channel closed.")
					return
				}

				var evt TenantOrderDBReadyEvent
				if err := json.Unmarshal(d.Body, &evt); err != nil {
					log.Printf("TenantOrderDBReadyConsumer Error: Bad payload: %v", err)
					_ = d.Nack(false, false)
					continue
				}

				log.Printf("TenantOrderDBReadyConsumer: Received ready event for tenant='%s' service='%s' host='%s'",
					evt.TenantID, evt.ServiceName, evt.DBHost)

				err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
					return c.workspaceService.HandleInfrastructureUpdate(txCtx, service.InfraUpdateInput{
						TenantID:    evt.TenantID,
						ServiceName: evt.ServiceName,
						DBHost:      evt.DBHost,
						DBPort:      evt.DBPort,
						DBName:      evt.DBName,
						DBUser:      evt.DBUser,
						SchemaName:  evt.SchemaName,
					})
				})

				if err != nil {
					log.Printf("TenantOrderDBReadyConsumer Error: Failed to update infrastructure state for tenant='%s': %v", evt.TenantID, err)
					_ = d.Nack(false, true) // requeue
					continue
				}

				_ = d.Ack(false)
				log.Printf("TenantOrderDBReadyConsumer: Infrastructure routing state updated & tenant activated for tenant='%s'", evt.TenantID)
			}
		}
	}()

	return nil
}
