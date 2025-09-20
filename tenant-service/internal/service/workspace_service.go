package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"tenant-service/internal/domain"
	"tenant-service/internal/publisher"
	"tenant-service/internal/repository"
	"tenant-service/internal/utils"
	"tenant-service/internal/worker"
)

var requiredServices = []string{"order-service"}

type RegisterWorkspaceInput struct {
	OwnerEmail string
	OwnerName  string
	Plan       string // "shared" | "dedicated"
	TenantName string
}

type RegisterWorkspaceOutput struct {
	TenantID string `json:"tenant_id"`
	Status   string `json:"status"`
}

type InfraUpdateInput struct {
	TenantID    string
	ServiceName string
	DSN         string
	SchemaName  string
}

type DSNOutput struct {
	DSN        string `json:"dsn"`
	SchemaName string `json:"schema_name"`
}

type WorkspaceService interface {
	RegisterWorkspace(ctx context.Context, input RegisterWorkspaceInput) (*RegisterWorkspaceOutput, error)
	HandleInfrastructureUpdate(ctx context.Context, input InfraUpdateInput) error
	GetServiceDSN(ctx context.Context, tenantID, serviceName string) (*DSNOutput, error)
}

type workspaceService struct {
	controlRepo  repository.ControlPlaneRepository
	outboxRepo   repository.OutboxRepository
	outboxWorker *worker.OutboxWorker
}

type WorkspaceServiceParams struct {
	ControlRepo  repository.ControlPlaneRepository
	OutboxRepo   repository.OutboxRepository
	OutboxWorker *worker.OutboxWorker
}

func NewWorkspaceService(params WorkspaceServiceParams) WorkspaceService {
	return &workspaceService{
		controlRepo:  params.ControlRepo,
		outboxRepo:   params.OutboxRepo,
		outboxWorker: params.OutboxWorker,
	}
}

func (s *workspaceService) RegisterWorkspace(ctx context.Context, input RegisterWorkspaceInput) (*RegisterWorkspaceOutput, error) {
	plan := domain.Plan(strings.ToLower(input.Plan))
	if !plan.IsValid() {
		return nil, fmt.Errorf("invalid plan '%s': must be '%s' or '%s'", input.Plan, domain.PlanShared, domain.PlanDedicated)
	}

	tenantID := domain.GenerateTenantID()
	slug := utils.SanitizeSlug(input.TenantName)
	outboxID := domain.GenerateOutboxID()

	evt := publisher.WorkspaceInitiatedEvent{
		EventID:    outboxID,
		TenantID:   tenantID,
		Plan:       plan.String(),
		OwnerEmail: input.OwnerEmail,
		OwnerName:  input.OwnerName,
	}
	payloadBytes, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal WorkspaceInitiated event: %w", err)
	}

	if err := s.controlRepo.CreateTenant(ctx, repository.CreateTenantInput{
		ID:         tenantID,
		Name:       input.TenantName,
		Slug:       slug,
		OwnerEmail: input.OwnerEmail,
		OwnerName:  input.OwnerName,
		Plan:       plan.String(),
	}); err != nil {
		return nil, fmt.Errorf("failed to create tenant record: %w", err)
	}

	outboxMsg := repository.OutboxMessage{
		ID:            outboxID,
		TenantID:      &tenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   tenantID,
		EventType:     "workspace.initiated",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepo.CreateOutboxMessage(ctx, outboxMsg); err != nil {
		return nil, fmt.Errorf("failed to stage workspace.initiated outbox event: %w", err)
	}

	log.Printf("WorkspaceService: Registered tenant_id='%s' slug='%s' plan='%s' outbox_id='%s'",
		tenantID, slug, plan, outboxID)

	s.outboxWorker.Poke()
	return &RegisterWorkspaceOutput{TenantID: tenantID}, nil
}

func (s *workspaceService) HandleInfrastructureUpdate(ctx context.Context, input InfraUpdateInput) error {
	if err := s.controlRepo.UpsertServiceInfrastructure(ctx,
		input.TenantID, input.ServiceName, input.DSN, input.SchemaName,
	); err != nil {
		return fmt.Errorf("failed to upsert service infrastructure: %w", err)
	}

	log.Printf("WorkspaceService: %s checked in for tenant_id='%s'", input.ServiceName, input.TenantID)

	pendingCount, err := s.controlRepo.GetPendingServiceCount(ctx, input.TenantID, requiredServices)
	if err != nil {
		return fmt.Errorf("failed to check pending service count: %w", err)
	}

	if pendingCount > 0 {
		log.Printf("WorkspaceService: %d required service(s) still pending for tenant_id='%s'", pendingCount, input.TenantID)
		return nil
	}

	tenant, err := s.controlRepo.GetTenantByID(ctx, input.TenantID)
	if err != nil {
		return fmt.Errorf("failed to fetch tenant for activation: %w", err)
	}

	outboxID := domain.GenerateOutboxID()
	readyEvt := publisher.WorkspaceReadyEvent{
		EventID:    outboxID,
		TenantID:   input.TenantID,
		OwnerEmail: tenant.OwnerEmail,
	}
	payloadBytes, err := json.Marshal(readyEvt)
	if err != nil {
		return fmt.Errorf("failed to marshal WorkspaceReady event: %w", err)
	}

	if err := s.controlRepo.ActivateTenant(ctx, input.TenantID); err != nil {
		return fmt.Errorf("failed to activate tenant: %w", err)
	}

	outboxMsg := repository.OutboxMessage{
		ID:            outboxID,
		TenantID:      &input.TenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   input.TenantID,
		EventType:     "workspace.ready",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepo.CreateOutboxMessage(ctx, outboxMsg); err != nil {
		return fmt.Errorf("failed to stage workspace.ready outbox event: %w", err)
	}

	log.Printf("WorkspaceService: Workspace ACTIVE for tenant_id='%s' — WorkspaceReady staged.", input.TenantID)
	s.outboxWorker.Poke()
	return nil
}

func (s *workspaceService) GetServiceDSN(ctx context.Context, tenantID, serviceName string) (*DSNOutput, error) {
	dsn, schemaName, err := s.controlRepo.GetServiceDSN(ctx, tenantID, serviceName)
	if err != nil {
		return nil, err
	}
	return &DSNOutput{DSN: dsn, SchemaName: schemaName}, nil
}
