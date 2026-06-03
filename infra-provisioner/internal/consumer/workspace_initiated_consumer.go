package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"infra-provisioner/internal/crypto"
	"infra-provisioner/internal/domain"
	"infra-provisioner/internal/infrastructure/rabbitmq"
)

// InfrastructureEventPublisher is the consumer-side interface expected by WorkspaceInitiatedConsumer.
type InfrastructureEventPublisher interface {
	PublishInfrastructureProvisioned(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error
	PublishTenantMigrationFailed(ctx context.Context, evt domain.TenantMigrationFailedEvent) error
}

// Provisioner is the consumer-side interface expected by WorkspaceInitiatedConsumer.
type Provisioner interface {
	ProvisionDedicatedContainer(ctx context.Context, tenantID, infraMasterSecret string, domainSecrets map[string]string) (host string, port int, dbName, dbUser string, err error)
}

// Migrator is the consumer-side interface expected by WorkspaceInitiatedConsumer for data migration & schema locking.
type Migrator interface {
	CheckSchemaExists(ctx context.Context, sharedDSN, schemaName string) (bool, error)
	LockSchema(ctx context.Context, sharedDSN, schemaName, lockedSchemaName string) error
	RestoreSchema(ctx context.Context, sharedDSN, lockedSchemaName, originalSchemaName string) error
	MigrateData(ctx context.Context, sourceHost string, sourcePort int, sourceUser, sourcePass, sourceDB, lockedSourceSchema string, targetHost string, targetPort int, targetUser, targetPass, targetDB string) error
	DestroyContainer(ctx context.Context, containerName string) error
}

type WorkspaceInitiatedConsumer struct {
	client            *rabbitmq.Client
	publisher         InfrastructureEventPublisher
	provisioner       Provisioner
	migrator          Migrator
	infraMasterSecret string
	domainSecrets     map[string]string
	sharedDBHost      string
	sharedDBPass      string
}

type WorkspaceInitiatedConsumerParams struct {
	Client            *rabbitmq.Client
	Publisher         InfrastructureEventPublisher
	Provisioner       Provisioner
	Migrator          Migrator
	InfraMasterSecret string
	DomainSecrets     map[string]string
	SharedDBHost      string
	SharedDBPass      string
}

type Params = WorkspaceInitiatedConsumerParams

func NewWorkspaceInitiatedConsumer(params WorkspaceInitiatedConsumerParams) (*WorkspaceInitiatedConsumer, error) {
	sharedHost := params.SharedDBHost
	if sharedHost == "" {
		sharedHost = "postgres"
	}
	sharedPass := params.SharedDBPass
	if sharedPass == "" {
		sharedPass = "postgres"
	}

	domainSec := params.DomainSecrets
	if len(domainSec) == 0 {
		domainSec = map[string]string{
			"order_db": "default_shared_db_secret_key",
		}
	}

	consumer := &WorkspaceInitiatedConsumer{
		client:            params.Client,
		publisher:         params.Publisher,
		provisioner:       params.Provisioner,
		migrator:          params.Migrator,
		infraMasterSecret: params.InfraMasterSecret,
		domainSecrets:     domainSec,
		sharedDBHost:      sharedHost,
		sharedDBPass:      sharedPass,
	}

	if err := consumer.setupTopology(); err != nil {
		return nil, err
	}

	return consumer, nil
}

func (c *WorkspaceInitiatedConsumer) setupTopology() error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}

	if err := c.client.DeclareAndBindQueue(
		domain.QueueInfraProvisionerWorkspace, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceInitiated,
	); err != nil {
		return fmt.Errorf("failed to bind queue '%s': %w", domain.QueueInfraProvisionerWorkspace, err)
	}

	return nil
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
	if err := c.setupTopology(); err != nil {
		return err
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

			_ = c.handleDelivery(appCtx, d)
		}
	}
}

func (c *WorkspaceInitiatedConsumer) handleDelivery(ctx context.Context, d rabbitmq.Delivery) error {
	var evt domain.WorkspaceInitiatedEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		log.Printf("WorkspaceInitiatedConsumer Error: Bad payload JSON: %v", err)
		_ = d.Nack(false, false) // unrecoverable bad JSON
		return err
	}

	log.Printf("WorkspaceInitiatedConsumer: Processing infrastructure for tenant='%s' plan='%s'", evt.TenantID, evt.Plan)

	provEvent, err := c.handleProvisioning(ctx, evt)
	if err != nil {
		log.Printf("WorkspaceInitiatedConsumer Error: Provisioning failed for tenant='%s': %v", evt.TenantID, err)
		_ = d.Nack(false, true) // requeue for retry
		return err
	}

	// Publish infrastructure.provisioned event via Publisher Adapter
	if err := c.publisher.PublishInfrastructureProvisioned(ctx, *provEvent); err != nil {
		log.Printf("WorkspaceInitiatedConsumer Error: Failed to publish '%s' for tenant='%s': %v", domain.RoutingKeyInfrastructureProvisioned, evt.TenantID, err)
		_ = d.Nack(false, true)
		return err
	}

	_ = d.Ack(false)
	log.Printf("WorkspaceInitiatedConsumer: Published '%s' for tenant='%s' (host=%s, schema=%s)",
		domain.RoutingKeyInfrastructureProvisioned, evt.TenantID, provEvent.DBHost, provEvent.SchemaName)
	return nil
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

		containerName := fmt.Sprintf("postgres-tenant-%s", sanitizeTenantID(evt.TenantID))
		schemaName := fmt.Sprintf("%s_order_db", sanitizeTenantID(evt.TenantID))
		lockedSchemaName := fmt.Sprintf("%s_locked", schemaName)
		sharedDSN := fmt.Sprintf("host=%s port=5432 user=postgres password=%s dbname=shared_db sslmode=disable", c.sharedDBHost, c.sharedDBPass)

		if c.migrator != nil {
			originalExists, chkErr := c.migrator.CheckSchemaExists(ctx, sharedDSN, schemaName)
			lockedExists, lockChkErr := c.migrator.CheckSchemaExists(ctx, sharedDSN, lockedSchemaName)

			// RabbitMQ delivers at-least-once: an ack-loss redelivery may re-enter this branch
			// after the schema lock already succeeded. The locked-schema existence check makes
			// the pipeline idempotent — resume instead of re-locking or skipping the copy.
			if chkErr == nil && lockChkErr == nil && (originalExists || lockedExists) {
				log.Printf("WorkspaceInitiatedConsumer: Data migration required for tenant='%s' (original_exists=%v locked_exists=%v).", evt.TenantID, originalExists, lockedExists)

				// Step 1: Deterministic schema lock (ALTER SCHEMA ... RENAME TO ..._locked).
				// Skipped when a prior delivery already acquired the lock.
				if !lockedExists {
					if lockErr := c.migrator.LockSchema(ctx, sharedDSN, schemaName, lockedSchemaName); lockErr != nil {
						log.Printf("WorkspaceInitiatedConsumer Error: Schema lock failed for tenant='%s': %v — executing compensating rollback", evt.TenantID, lockErr)
						_ = c.migrator.DestroyContainer(ctx, containerName)
						_ = c.publisher.PublishTenantMigrationFailed(ctx, domain.TenantMigrationFailedEvent{
							EventID:  evt.EventID,
							TenantID: evt.TenantID,
							Reason:   lockErr.Error(),
						})
						return nil, lockErr
					}
				} else {
					log.Printf("WorkspaceInitiatedConsumer: Schema already locked ('%s'); resuming data migration pipeline.", lockedSchemaName)
				}

				// Step 2: Data copy (pg_dump | sed | psql) — idempotent via --clean --if-exists
				targetPass := crypto.DeriveTenantDBPassword(c.domainSecrets["order_db"], evt.TenantID)
				if migErr := c.migrator.MigrateData(ctx,
					c.sharedDBHost, 5432, "postgres", c.sharedDBPass, "shared_db", lockedSchemaName,
					host, port, dbUser, targetPass, dbName,
				); migErr != nil {
					log.Printf("WorkspaceInitiatedConsumer Error: Data migration failed for tenant='%s': %v — executing schema restore & compensating rollback", evt.TenantID, migErr)
					_ = c.migrator.RestoreSchema(ctx, sharedDSN, lockedSchemaName, schemaName)
					_ = c.migrator.DestroyContainer(ctx, containerName)
					_ = c.publisher.PublishTenantMigrationFailed(ctx, domain.TenantMigrationFailedEvent{
						EventID:  evt.EventID,
						TenantID: evt.TenantID,
						Reason:   migErr.Error(),
					})
					return nil, migErr
				}
			}
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
