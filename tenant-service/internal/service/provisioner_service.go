package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
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

type ProvisionTenantOutput struct {
	TenantID   string
	SchemaName string
}

type ProvisionerService interface {
	ProvisionTenant(ctx context.Context, input ProvisionTenantInput) (*ProvisionTenantOutput, error)
}

type provisionerService struct {
	repo         repository.ProvisionerRepository
	outboxRepo   repository.OutboxRepository
	outboxWorker *worker.OutboxWorker
}

func NewProvisionerService(
	repo repository.ProvisionerRepository,
	outboxRepo repository.OutboxRepository,
	outboxWorker *worker.OutboxWorker,
) ProvisionerService {
	return &provisionerService{
		repo:         repo,
		outboxRepo:   outboxRepo,
		outboxWorker: outboxWorker,
	}
}

// stageOutboxEvent marshals a TenantProvisionedEvent and writes it into the outbox
// table using whatever DB/Tx executor is carried in ctx.
func (s *provisionerService) stageOutboxEvent(ctx context.Context, tenantID, tenantSlug, userID, placementType, schemaName, dbDSN string) (string, error) {
	outboxID := "outbox_" + uuid.New().String()
	evt := publisher.TenantProvisionedEvent{
		EventID:       outboxID,
		TenantID:      tenantID,
		TenantSlug:    tenantSlug,
		UserID:        userID,
		PlacementType: placementType,
		SchemaName:    schemaName,
		DbDSN:         dbDSN,
	}
	payloadBytes, err := json.Marshal(evt)
	if err != nil {
		return "", fmt.Errorf("failed to marshal TenantProvisioned event: %w", err)
	}

	outboxMsg := repository.OutboxMessage{
		ID:            outboxID,
		TenantID:      &tenantID,
		AggregateType: "TENANT",
		AggregateID:   tenantID,
		EventType:     "tenant.provisioned",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepo.CreateOutboxMessage(ctx, outboxMsg); err != nil {
		return "", fmt.Errorf("failed to stage outbox event: %w", err)
	}
	return outboxID, nil
}

func (s *provisionerService) ProvisionTenant(ctx context.Context, input ProvisionTenantInput) (*ProvisionTenantOutput, error) {
	placement := input.PlacementType
	if placement == "" {
		placement = "SHARED"
	}

	tenantID := utils.SanitizeSchemaName(input.TenantSlug)
	targetSchema := tenantID
	if placement == "DEDICATED" {
		targetSchema = "public"
	}

	log.Printf("ProvisionerService: Provisioning %s target schema '%s' for slug '%s'...", placement, targetSchema, input.TenantSlug)

	// 1. Create schema if SHARED (for DEDICATED 'public' schema already exists)
	if placement == "SHARED" {
		if err := s.repo.CreateSchema(ctx, targetSchema); err != nil {
			return nil, err
		}
	}

	// 2. Execute migration script on target schema
	if err := s.repo.ExecuteMigration(ctx, targetSchema, "migrations/001_init_tenant_schema.sql"); err != nil {
		return nil, err
	}

	// 3. Seed owner member
	if err := s.repo.SeedOwnerMember(ctx, targetSchema, input.UserID, input.Name, input.Email); err != nil {
		return nil, err
	}

	// 4. Stage TenantProvisioned event into local outbox (uses transaction in ctx)
	outboxID, err := s.stageOutboxEvent(ctx, tenantID, input.TenantSlug, input.UserID, placement, targetSchema, input.DbDSN)
	if err != nil {
		return nil, err
	}

	log.Printf("ProvisionerService: Successfully provisioned %s schema '%s' and staged outbox_id='%s'.", placement, targetSchema, outboxID)

	// 5. Wake up background outbox worker
	s.outboxWorker.Poke()

	return &ProvisionTenantOutput{
		TenantID:   tenantID,
		SchemaName: targetSchema,
	}, nil
}
