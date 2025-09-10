package consumer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/publisher"
	"user-service/internal/service"
	"user-service/internal/txctx"
)

const (
	RoutingKeyTenantProvisioned   = "tenant.provisioned"
	QueueUserServiceTenantProvisioned = "user_service_tenant_provisioned"
)

type TenantProvisionedEvent struct {
	EventID       string `json:"event_id"`
	TenantID      string `json:"tenant_id"`
	TenantSlug    string `json:"tenant_slug"`
	UserID        string `json:"user_id"`
	PlacementType string `json:"placement_type"`
	SchemaName    string `json:"schema_name"`
	DbDSN         string `json:"db_dsn"`
}

type TenantProvisionedConsumer struct {
	db          *sql.DB
	client      *rabbitmq.Client
	userService service.UserService
}

type TenantProvisionedConsumerParams struct {
	DB          *sql.DB
	Client      *rabbitmq.Client
	UserService service.UserService
}

func NewTenantProvisionedConsumer(params TenantProvisionedConsumerParams) (*TenantProvisionedConsumer, error) {
	if err := params.Client.DeclareExchange(publisher.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	if err := params.Client.DeclareAndBindQueue(QueueUserServiceTenantProvisioned, publisher.ExchangeCompanyEvents, RoutingKeyTenantProvisioned); err != nil {
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	return &TenantProvisionedConsumer{
		db:          params.DB,
		client:      params.Client,
		userService: params.UserService,
	}, nil
}

func (c *TenantProvisionedConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		QueueUserServiceTenantProvisioned, // queue
		"user-service-worker",              // consumer tag
		false,                               // auto-ack
		false,                               // exclusive
		false,                               // no-local
		false,                               // no-wait
		nil,                                 // args
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue %s: %w", QueueUserServiceTenantProvisioned, err)
	}

	log.Printf("User Service worker listening for events on queue '%s'...", QueueUserServiceTenantProvisioned)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("TenantProvisionedConsumer: Context cancelled, shutting down.")
				return
			case d, ok := <-msgs:
				if !ok {
					log.Printf("TenantProvisionedConsumer: Message channel closed.")
					return
				}
				log.Printf("Received TenantProvisioned message from queue '%s'", QueueUserServiceTenantProvisioned)

				var evt TenantProvisionedEvent
				if err := json.Unmarshal(d.Body, &evt); err != nil {
					log.Printf("Error unmarshaling TenantProvisioned payload: %v", err)
					d.Nack(false, false)
					continue
				}

				// Manage Transaction Boundary at Consumer Layer
				tx, err := c.db.BeginTx(ctx, nil)
				if err != nil {
					log.Printf("Error starting transaction in consumer: %v", err)
					d.Nack(false, true)
					continue
				}

				msgCtx := txctx.WithTx(ctx, tx)
				input := service.HandleTenantProvisionedInput{
					EventID:       evt.EventID,
					TenantID:      evt.TenantID,
					TenantSlug:    evt.TenantSlug,
					UserID:        evt.UserID,
					PlacementType: evt.PlacementType,
					SchemaName:    evt.SchemaName,
					DbDSN:         evt.DbDSN,
				}

				if err := c.userService.HandleTenantProvisioned(msgCtx, input); err != nil {
					tx.Rollback()
					log.Printf("Error updating tenant placement: %v", err)
					d.Nack(false, true)
					continue
				}

				if err := tx.Commit(); err != nil {
					tx.Rollback()
					log.Printf("Error committing transaction: %v", err)
					d.Nack(false, true)
					continue
				}

				d.Ack(false)
			}
		}
	}()

	return nil
}
