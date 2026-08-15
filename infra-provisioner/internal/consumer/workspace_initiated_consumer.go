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

type WorkspaceInitiatedConsumer struct {
	client                       AMQPClient
	infrastructureEventPublisher InfrastructureEventPublisher
	provisioner                  Provisioner
	migrator                     Migrator
	infraMasterSecret            string
	domainSecrets                map[string]string
	sharedDBHost                 string
	sharedDBPass                 string
	isolationMode                string
}

type WorkspaceInitiatedConsumerParams struct {
	Client                     AMQPClient
	InfrastructureEventHandler InfrastructureEventPublisher
	Provisioner                Provisioner
	Migrator                   Migrator
	InfraMasterSecret          string
	DomainSecrets              map[string]string
	SharedDBHost               string
	SharedDBPass               string
	IsolationMode              string
}

type Params = WorkspaceInitiatedConsumerParams

func NewWorkspaceInitiatedConsumer(params WorkspaceInitiatedConsumerParams) *WorkspaceInitiatedConsumer {
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

	isolationMode := params.IsolationMode
	if isolationMode == "" {
		isolationMode = "container"
	}

	return &WorkspaceInitiatedConsumer{
		client:                       params.Client,
		infrastructureEventPublisher: params.InfrastructureEventHandler,
		provisioner:                  params.Provisioner,
		migrator:                     params.Migrator,
		infraMasterSecret:            params.InfraMasterSecret,
		domainSecrets:                domainSec,
		sharedDBHost:                 sharedHost,
		sharedDBPass:                 sharedPass,
		isolationMode:                isolationMode,
	}
}

func (c *WorkspaceInitiatedConsumer) setupTopology() error {
	if err := c.client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return fmt.Errorf("failed to declare exchange '%s': %w", domain.ExchangeCompanyEvents, err)
	}

	if err := c.client.DeclareAndBindQueue(domain.QueueInfraProvisionerWorkspace, domain.ExchangeCompanyEvents, domain.RoutingKeyWorkspaceInitiated); err != nil {
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

	msgs, err := c.client.Consume(
		domain.QueueInfraProvisionerWorkspace,
		"infra-provisioner-worker",
	)
	if err != nil {
		return fmt.Errorf("failed to start consume: %w", err)
	}

	log.Printf("InfraProvisioner: Listening for '%s' events on queue '%s'...", domain.RoutingKeyWorkspaceInitiated, domain.QueueInfraProvisionerWorkspace)

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

	deliveryCount := getDeliveryCount(d.Headers)
	if deliveryCount >= 3 {
		log.Printf("[DLQ] WorkspaceInitiatedConsumer: Max delivery count reached for event_id='%s' tenant_id='%s' (delivery_count=%d). Routing to DLQ.",
			evt.EventID, evt.TenantID, deliveryCount)
		_ = d.Nack(false, false)
		return errors.New("max delivery count reached")
	}

	log.Printf("WorkspaceInitiatedConsumer: Processing infrastructure for tenant='%s' plan='%s'", evt.TenantID, evt.Plan)

	provEvent, err := c.handleProvisioning(ctx, evt)
	if err != nil {
		log.Printf("WorkspaceInitiatedConsumer Error: Provisioning failed for tenant='%s': %v", evt.TenantID, err)
		_ = d.Nack(false, true) // requeue for retry
		return err
	}

	// Publish infrastructure.provisioned event via Publisher Adapter
	if err := c.infrastructureEventPublisher.PublishInfrastructureProvisioned(ctx, *provEvent); err != nil {
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
		return c.handleSharedProvisioning(ctx, evt)
	case "dedicated":
		return c.handleDedicatedProvisioning(ctx, evt)
	default:
		return nil, fmt.Errorf("unknown plan '%s' for tenant '%s'", evt.Plan, evt.TenantID)
	}
}

// handleSharedProvisioning returns routing to the shared cluster. When the
// tenant previously ran on the dedicated plan, it first migrates the tenant's
// data back into the shared schema (downgrade path) and then releases the
// dedicated resources (container or same-instance database).
func (c *WorkspaceInitiatedConsumer) handleSharedProvisioning(ctx context.Context, evt domain.WorkspaceInitiatedEvent) (*domain.InfrastructureProvisionedEvent, error) {
	schemaName := fmt.Sprintf("%s_order_db", sanitizeTenantID(evt.TenantID))
	sharedDSN := fmt.Sprintf("host=%s port=5432 user=postgres password=%s dbname=shared_db sslmode=disable", c.sharedDBHost, c.sharedDBPass)

	if c.migrator != nil {
		sameInstance := strings.EqualFold(c.isolationMode, "same_instance")

		// Dedicated source: a per-tenant database in the shared instance, or a dedicated container.
		var sourceHost string
		var sourcePort int
		var sourceUser, sourcePass, sourceDB string
		var cleanup func() error

		if sameInstance {
			roleName := fmt.Sprintf("%s_order_user", sanitizeTenantID(evt.TenantID))
			rolePass := crypto.DeriveTenantDBPassword(c.domainSecrets["order_db"], evt.TenantID)
			sourceHost, sourcePort, sourceUser, sourcePass, sourceDB = c.sharedDBHost, 5432, roleName, rolePass, schemaName
			cleanup = func() error {
				return c.migrator.DropTenantDatabase(ctx, c.sharedDBHost, 5432, "postgres", c.sharedDBPass, schemaName, roleName)
			}
		} else {
			containerName := fmt.Sprintf("postgres-tenant-%s", sanitizeTenantID(evt.TenantID))
			sourceHost, sourcePort, sourceUser, sourcePass, sourceDB = containerName, 5432, "order_user", crypto.DeriveTenantDBPassword(c.domainSecrets["order_db"], evt.TenantID), "order_db"
			cleanup = func() error {
				return c.migrator.DestroyContainer(ctx, containerName)
			}
		}

		sourceDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			sourceHost, sourcePort, sourceUser, sourcePass, sourceDB)

		// If the dedicated source is reachable and holds order rows, copy them back.
		hasRows, chkErr := c.migrator.TableHasRows(ctx, sourceDSN, "public", "orders")
		if chkErr == nil && hasRows {
			log.Printf("WorkspaceInitiatedConsumer: Dedicated data detected for tenant='%s'. Migrating back to shared schema '%s'...", evt.TenantID, schemaName)
			if migErr := c.migrator.MigrateData(ctx,
				sourceHost, sourcePort, sourceUser, sourcePass, sourceDB, "public",
				c.sharedDBHost, 5432, "postgres", c.sharedDBPass, "shared_db", schemaName,
			); migErr != nil {
				return nil, fmt.Errorf("failed to migrate data back to shared for tenant '%s': %w", evt.TenantID, migErr)
			}
			// Best-effort cleanup of a leftover locked schema from a previous upgrade.
			_ = c.migrator.DropSchemaIfExists(ctx, sharedDSN, schemaName+"_locked")
		}

		// The dedicated resource is no longer the active data plane after a
		// downgrade; release it once the shared provisioning succeeds.
		_ = cleanup()
	}

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
}

// handleDedicatedProvisioning provisions a dedicated database for the tenant —
// either a separate container (container mode) or a per-tenant database inside
// the shared instance (same_instance mode). When the tenant has a shared schema
// (or a previously-locked schema from a redelivered event), it locks the schema,
// migrates the data into the dedicated target, then removes the locked schema.
func (c *WorkspaceInitiatedConsumer) handleDedicatedProvisioning(ctx context.Context, evt domain.WorkspaceInitiatedEvent) (*domain.InfrastructureProvisionedEvent, error) {
	schemaName := fmt.Sprintf("%s_order_db", sanitizeTenantID(evt.TenantID))
	lockedSchemaName := fmt.Sprintf("%s_locked", schemaName)
	sharedDSN := fmt.Sprintf("host=%s port=5432 user=postgres password=%s dbname=shared_db sslmode=disable", c.sharedDBHost, c.sharedDBPass)
	targetPass := crypto.DeriveTenantDBPassword(c.domainSecrets["order_db"], evt.TenantID)

	var host string
	var port int
	var dbName, dbUser string

	// Cleanup resource that must be destroyed on lock/migration failure.
	var rollbackCleanup func()

	sameInstance := strings.EqualFold(c.isolationMode, "same_instance")
	if sameInstance {
		roleName := fmt.Sprintf("%s_order_user", sanitizeTenantID(evt.TenantID))
		host, port, dbName, dbUser = c.sharedDBHost, 5432, schemaName, roleName
		if c.migrator != nil {
			if err := c.migrator.ProvisionTenantDatabase(ctx, c.sharedDBHost, 5432, "postgres", c.sharedDBPass, schemaName, roleName, targetPass); err != nil {
				return nil, fmt.Errorf("failed to provision tenant database for tenant '%s': %w", evt.TenantID, err)
			}
		}
		rollbackCleanup = func() {
			_ = c.migrator.DropTenantDatabase(ctx, c.sharedDBHost, 5432, "postgres", c.sharedDBPass, schemaName, roleName)
		}
	} else {
		var err error
		host, port, dbName, dbUser, err = c.provisioner.ProvisionDedicatedContainer(ctx, evt.TenantID, c.infraMasterSecret, c.domainSecrets)
		if err != nil {
			return nil, err
		}
		containerName := fmt.Sprintf("postgres-tenant-%s", sanitizeTenantID(evt.TenantID))
		rollbackCleanup = func() {
			_ = c.migrator.DestroyContainer(ctx, containerName)
		}
	}

	if c.migrator != nil {
		schemaExists, _ := c.migrator.CheckSchemaExists(ctx, sharedDSN, schemaName)
		lockedExists, _ := c.migrator.CheckSchemaExists(ctx, sharedDSN, lockedSchemaName)

		if schemaExists || lockedExists {
			log.Printf("WorkspaceInitiatedConsumer: Shared schema detected for tenant='%s'. Initiating data migration to dedicated target...", evt.TenantID)

			// Lock only if not already locked; an already-locked schema means a
			// redelivered workspace.initiated is being resumed, so do not re-rename.
			if !lockedExists {
				if lockErr := c.migrator.LockSchema(ctx, sharedDSN, schemaName, lockedSchemaName); lockErr != nil {
					log.Printf("WorkspaceInitiatedConsumer Error: Schema lock failed for tenant='%s': %v — executing compensating rollback", evt.TenantID, lockErr)
					rollbackCleanup()
					_ = c.infrastructureEventPublisher.PublishTenantMigrationFailed(ctx, domain.TenantMigrationFailedEvent{
						EventID:  evt.EventID,
						TenantID: evt.TenantID,
						Reason:   lockErr.Error(),
					})
					return nil, lockErr
				}
			}

			if migErr := c.migrator.MigrateData(ctx,
				c.sharedDBHost, 5432, "postgres", c.sharedDBPass, "shared_db", lockedSchemaName,
				host, port, dbUser, targetPass, dbName, "public",
			); migErr != nil {
				log.Printf("WorkspaceInitiatedConsumer Error: Data migration failed for tenant='%s': %v — executing schema restore & compensating rollback", evt.TenantID, migErr)
				_ = c.migrator.RestoreSchema(ctx, sharedDSN, lockedSchemaName, schemaName)
				rollbackCleanup()
				_ = c.infrastructureEventPublisher.PublishTenantMigrationFailed(ctx, domain.TenantMigrationFailedEvent{
					EventID:  evt.EventID,
					TenantID: evt.TenantID,
					Reason:   migErr.Error(),
				})
				return nil, migErr
			}

			// Data now lives in the dedicated target; remove the leftover locked schema.
			_ = c.migrator.DropSchemaIfExists(ctx, sharedDSN, lockedSchemaName)
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
}

func sanitizeTenantID(id string) string {
	return strings.ReplaceAll(strings.ToLower(id), "-", "_")
}
