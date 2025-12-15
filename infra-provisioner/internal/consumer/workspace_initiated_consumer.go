package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"infra-provisioner/internal/domain"
	"infra-provisioner/internal/infrastructure/rabbitmq"
)

// InfrastructureEventPublisher is the consumer-side interface expected by WorkspaceInitiatedConsumer.
type InfrastructureEventPublisher interface {
	PublishInfrastructureProvisioned(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error
}

// Provisioner is the consumer-side interface expected by WorkspaceInitiatedConsumer.
type Provisioner interface {
	ProvisionDedicatedContainer(ctx context.Context, tenantID, infraMasterSecret string, domainSecrets map[string]string) (host string, port int, dbName, dbUser string, err error)
}

type WorkspaceInitiatedConsumer struct {
	client            *rabbitmq.Client
	publisher         InfrastructureEventPublisher
	provisioner       Provisioner
	infraMasterSecret string
	domainSecrets     map[string]string
	sharedDBHost      string
}

type Params struct {
	Client            *rabbitmq.Client
	Publisher         InfrastructureEventPublisher
	Provisioner       Provisioner
	InfraMasterSecret string
	DomainSecrets     map[string]string
	SharedDBHost      string
}

func NewWorkspaceInitiatedConsumer(params Params) (*WorkspaceInitiatedConsumer, error) {
	if err := params.Client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}

	if err := params.Client.DeclareAndBindQueue(
		domain.QueueInfraProvisionerWorkspace, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceInitiated,
	); err != nil {
		return nil, fmt.Errorf("failed to bind queue '%s': %w", domain.QueueInfraProvisionerWorkspace, err)
	}

	sharedHost := params.SharedDBHost
	if sharedHost == "" {
		sharedHost = "postgres"
	}

	domainSec := params.DomainSecrets
	if len(domainSec) == 0 {
		domainSec = map[string]string{
			"order_db": "default_shared_db_secret_key",
		}
	}

	return &WorkspaceInitiatedConsumer{
		client:            params.Client,
		publisher:         params.Publisher,
		provisioner:       params.Provisioner,
		infraMasterSecret: params.InfraMasterSecret,
		domainSecrets:     domainSec,
		sharedDBHost:      sharedHost,
	}, nil
}

func (c *WorkspaceInitiatedConsumer) Start(ctx context.Context) error {
	go func() {
		for {
			connCtx := c.client.ConnContext()

			err := c.runConsumerLoop(ctx, connCtx)

			if ctx.Err() != nil {
				return
			}

			log.Printf("WorkspaceInitiatedConsumer: connection context cancelled (%v); waiting for RabbitMQ reconnection...", err)

			if err := c.client.WaitUntilReady(ctx); err != nil {
				return
			}

			log.Println("WorkspaceInitiatedConsumer: reconnected; re-binding queue topology...")
		}
	}()

	return nil
}

func (c *WorkspaceInitiatedConsumer) runConsumerLoop(appCtx, connCtx context.Context) error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}

	if err := c.client.DeclareAndBindQueue(
		domain.QueueInfraProvisionerWorkspace, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceInitiated,
	); err != nil {
		return fmt.Errorf("failed to bind queue '%s': %w", domain.QueueInfraProvisionerWorkspace, err)
	}

	if c.client == nil || c.client.Channel == nil {
		return errors.New("channel is nil")
	}

	_ = c.client.Channel.Qos(1, 0, false)

	msgs, err := c.client.Channel.Consume(
		domain.QueueInfraProvisionerWorkspace,
		"infra-provisioner-worker",
		false, // manual ack
		false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("InfraProvisioner: Listening for '%s' events on queue '%s' (QoS prefetch=1)...", domain.RoutingKeyWorkspaceInitiated, domain.QueueInfraProvisionerWorkspace)

	for {
		select {
		case <-appCtx.Done():
			log.Printf("WorkspaceInitiatedConsumer: Context cancelled. Stopping consumer.")
			return appCtx.Err()

		case <-connCtx.Done():
			return connCtx.Err()

		case d, ok := <-msgs:
			if !ok {
				return errors.New("delivery channel closed")
			}

			var evt domain.WorkspaceInitiatedEvent
			if err := json.Unmarshal(d.Body, &evt); err != nil {
				log.Printf("WorkspaceInitiatedConsumer Error: Bad payload JSON: %v", err)
				_ = d.Nack(false, false) // unrecoverable bad JSON
				continue
			}

			log.Printf("WorkspaceInitiatedConsumer: Processing infrastructure for tenant='%s' plan='%s'", evt.TenantID, evt.Plan)

			provEvent, err := c.handleProvisioning(appCtx, evt)
			if err != nil {
				log.Printf("WorkspaceInitiatedConsumer Error: Provisioning failed for tenant='%s': %v", evt.TenantID, err)
				_ = d.Nack(false, true) // requeue for retry
				continue
			}

			// Publish infrastructure.provisioned event via Publisher Adapter
			if err := c.publisher.PublishInfrastructureProvisioned(appCtx, *provEvent); err != nil {
				log.Printf("WorkspaceInitiatedConsumer Error: Failed to publish '%s' for tenant='%s': %v", domain.RoutingKeyInfrastructureProvisioned, evt.TenantID, err)
				_ = d.Nack(false, true)
				continue
			}

			_ = d.Ack(false)
			log.Printf("WorkspaceInitiatedConsumer: Published '%s' for tenant='%s' (host=%s, schema=%s)",
				domain.RoutingKeyInfrastructureProvisioned, evt.TenantID, provEvent.DBHost, provEvent.SchemaName)
		}
	}
}

func (c *WorkspaceInitiatedConsumer) handleProvisioning(ctx context.Context, evt domain.WorkspaceInitiatedEvent) (*domain.InfrastructureProvisionedEvent, error) {
	switch strings.ToLower(evt.Plan) {
	case "shared":
		schemaName := fmt.Sprintf("%s_order_db", sanitizeTenantID(evt.TenantID))
		return &domain.InfrastructureProvisionedEvent{
			EventID:    evt.EventID,
			TenantID:   evt.TenantID,
			Plan:       "shared",
			DBHost:     c.sharedDBHost,
			DBPort:     5432,
			DBName:     "shared_db",
			DBUser:     "postgres",
			SchemaName: schemaName,
		}, nil

	case "dedicated":
		host, port, dbName, dbUser, err := c.provisioner.ProvisionDedicatedContainer(ctx, evt.TenantID, c.infraMasterSecret, c.domainSecrets)
		if err != nil {
			return nil, err
		}

		return &domain.InfrastructureProvisionedEvent{
			EventID:    evt.EventID,
			TenantID:   evt.TenantID,
			Plan:       "dedicated",
			DBHost:     host,
			DBPort:     port,
			DBName:     dbName,
			DBUser:     dbUser,
			SchemaName: "public",
		}, nil

	default:
		return nil, fmt.Errorf("unknown plan '%s' for tenant '%s'", evt.Plan, evt.TenantID)
	}
}

func sanitizeTenantID(id string) string {
	return strings.ReplaceAll(strings.ToLower(id), "-", "_")
}
