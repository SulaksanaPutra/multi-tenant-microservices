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
	TenantSlug string
	UserID     string
	Name       string
	Email      string
}

type TenantService interface {
	ProvisionTenant(ctx context.Context, input ProvisionTenantInput) (string, error)
}

type tenantService struct {
	dbClient     *postgres.Client
	repo         repository.ProvisionerRepository
	outboxRepo   repository.OutboxRepository
	outboxWorker *worker.OutboxWorker
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

func (s *tenantService) ProvisionTenant(ctx context.Context, input ProvisionTenantInput) (string, error) {
	schemaName := utils.SanitizeSchemaName(input.TenantSlug)

	log.Printf("TenantService: Provisioning schema '%s' for slug '%s' (owner: %s)...",
		schemaName, input.TenantSlug, input.Name)

	tx, err := s.dbClient.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to start provisioning transaction: %w", err)
	}
	defer tx.Rollback()

	if err := s.repo.CreateSchemaTx(ctx, tx, schemaName); err != nil {
		return "", err
	}

	migrationPath := "migrations/001_init_tenant_schema.sql"
	if err := s.repo.ExecuteMigrationTx(ctx, tx, schemaName, migrationPath); err != nil {
		return "", err
	}

	if err := s.repo.SeedOwnerMemberTx(ctx, tx, schemaName, input.UserID, input.Name, input.Email); err != nil {
		return "", err
	}

	evt := publisher.TenantProvisionedEvent{
		TenantID:   schemaName,
		TenantSlug: input.TenantSlug,
		UserID:     input.UserID,
	}
	payloadBytes, err := json.Marshal(evt)
	if err != nil {
		return "", fmt.Errorf("failed to marshal TenantProvisioned event payload: %w", err)
	}

	outboxID := "outbox_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	outboxMsg := repository.OutboxMessage{
		ID:            outboxID,
		TenantID:      &schemaName,
		AggregateType: "TENANT",
		AggregateID:   schemaName,
		EventType:     "tenant.provisioned",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepo.CreateOutboxMessage(ctx, tx, outboxMsg); err != nil {
		return "", fmt.Errorf("failed to stage outbox event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("failed to commit provisioning transaction: %w", err)
	}

	log.Printf("TenantService: Provisioned schema '%s' and staged outbox_id='%s' atomically.", schemaName, outboxID)

	s.outboxWorker.Poke()

	return schemaName, nil
}
