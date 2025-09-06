package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/publisher"
	"tenant-service/internal/repository"
	"tenant-service/internal/utils"
	"tenant-service/internal/worker"
)

type ProvisionTenantInput struct {
	TenantSlug    string
	UserID        string
	Name          string
	Email         string
	PlacementType string // "SHARED" or "DEDICATED"
	DbDSN         string // Connection string if DEDICATED
}

type TenantService interface {
	ProvisionTenant(ctx context.Context, input ProvisionTenantInput) (string, error)
	SetConnectionRegistry(registry *postgres.ConnectionRegistry)
}

type tenantService struct {
	dbClient     *postgres.Client
	repo         repository.ProvisionerRepository
	outboxRepo   repository.OutboxRepository
	outboxWorker *worker.OutboxWorker
	registry     *postgres.ConnectionRegistry
}

func NewTenantService(
	dbClient *postgres.Client,
	repo repository.ProvisionerRepository,
	outboxRepo repository.OutboxRepository,
	outboxWorker *worker.OutboxWorker,
) TenantService {
	return &tenantService{
		dbClient:     dbClient,
		repo:         repo,
		outboxRepo:   outboxRepo,
		outboxWorker: outboxWorker,
	}
}

func (s *tenantService) SetConnectionRegistry(registry *postgres.ConnectionRegistry) {
	s.registry = registry
}

func (s *tenantService) ProvisionTenant(ctx context.Context, input ProvisionTenantInput) (string, error) {
	placement := input.PlacementType
	if placement == "" {
		placement = "SHARED"
	}

	tenantID := utils.SanitizeSchemaName(input.TenantSlug)

	if placement == "DEDICATED" {
		log.Printf("TenantService: Provisioning DEDICATED database for tenant '%s' (DSN: %s)...", tenantID, input.DbDSN)

		if s.registry == nil {
			return "", fmt.Errorf("connection registry not initialized for dedicated tenant provisioning")
		}

		meta := &postgres.TenantMetadata{
			ID:            tenantID,
			Name:          input.Name,
			PlacementType: "DEDICATED",
			DbDSN:         input.DbDSN,
		}
		targetPool, err := s.registry.GetConnection(meta)
		if err != nil {
			return "", fmt.Errorf("failed to get dedicated DB connection: %w", err)
		}

		tx, err := targetPool.BeginTx(ctx, nil)
		if err != nil {
			return "", fmt.Errorf("failed to start dedicated DB tx: %w", err)
		}
		defer tx.Rollback()

		targetSchema := "public"
		if err := s.repo.ExecuteMigrationTx(ctx, tx, targetSchema, "migrations/001_init_tenant_schema.sql"); err != nil {
			return "", err
		}
		if err := s.repo.SeedOwnerMemberTx(ctx, tx, targetSchema, input.UserID, input.Name, input.Email); err != nil {
			return "", err
		}

		outboxID := "outbox_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
		evt := publisher.TenantProvisionedEvent{
			EventID:    outboxID,
			TenantID:   tenantID,
			TenantSlug: input.TenantSlug,
			UserID:     input.UserID,
		}
		payloadBytes, err := json.Marshal(evt)
		if err != nil {
			return "", fmt.Errorf("failed to marshal event: %w", err)
		}

		outboxMsg := repository.OutboxMessage{
			ID:            outboxID,
			TenantID:      &tenantID,
			AggregateType: "TENANT",
			AggregateID:   tenantID,
			EventType:     "tenant.provisioned",
			Payload:       payloadBytes,
		}
		if err := s.outboxRepo.CreateOutboxMessage(ctx, tx, outboxMsg); err != nil {
			return "", fmt.Errorf("failed to stage dedicated outbox event: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("failed to commit dedicated DB tx: %w", err)
		}

		// Save Control Plane metadata entry
		tenantUUID := "tenant_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
		_, err = s.dbClient.ExecContext(ctx, `
			INSERT INTO public.tenants (id, name, slug, owner_id, placement_type, db_dsn)
			VALUES ($1, $2, $3, $4, 'DEDICATED', $5)
			ON CONFLICT (slug) DO NOTHING;
		`, tenantUUID, input.Name, input.TenantSlug, input.UserID, input.DbDSN)
		if err != nil {
			log.Printf("TenantService Warning: Failed to insert tenant metadata into control plane: %v", err)
		}

		log.Printf("TenantService: Successfully provisioned DEDICATED database for tenant '%s' and staged outbox_id='%s'.", tenantID, outboxID)
		s.outboxWorker.Poke()
		return tenantID, nil
	}

	// Default SHARED schema provisioning
	log.Printf("TenantService: Provisioning SHARED schema '%s' for slug '%s'...", tenantID, input.TenantSlug)

	tx, err := s.dbClient.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to start provisioning transaction: %w", err)
	}
	defer tx.Rollback()

	if err := s.repo.CreateSchemaTx(ctx, tx, tenantID); err != nil {
		return "", err
	}
	if err := s.repo.ExecuteMigrationTx(ctx, tx, tenantID, "migrations/001_init_tenant_schema.sql"); err != nil {
		return "", err
	}
	if err := s.repo.SeedOwnerMemberTx(ctx, tx, tenantID, input.UserID, input.Name, input.Email); err != nil {
		return "", err
	}

	outboxID := "outbox_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	evt := publisher.TenantProvisionedEvent{
		EventID:    outboxID,
		TenantID:   tenantID,
		TenantSlug: input.TenantSlug,
		UserID:     input.UserID,
	}
	payloadBytes, err := json.Marshal(evt)
	if err != nil {
		return "", fmt.Errorf("failed to marshal event: %w", err)
	}

	outboxMsg := repository.OutboxMessage{
		ID:            outboxID,
		TenantID:      &tenantID,
		AggregateType: "TENANT",
		AggregateID:   tenantID,
		EventType:     "tenant.provisioned",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepo.CreateOutboxMessage(ctx, tx, outboxMsg); err != nil {
		return "", fmt.Errorf("failed to stage outbox event: %w", err)
	}

	// Save Control Plane metadata entry
	tenantUUID := "tenant_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	_, _ = tx.ExecContext(ctx, `
		INSERT INTO public.tenants (id, name, slug, owner_id, placement_type, schema_name)
		VALUES ($1, $2, $3, $4, 'SHARED', $5)
		ON CONFLICT (slug) DO NOTHING;
	`, tenantUUID, input.Name, input.TenantSlug, input.UserID, tenantID)

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("failed to commit provisioning transaction: %w", err)
	}

	log.Printf("TenantService: Provisioned SHARED schema '%s' and staged outbox_id='%s' atomically.", tenantID, outboxID)
	s.outboxWorker.Poke()
	return tenantID, nil
}
