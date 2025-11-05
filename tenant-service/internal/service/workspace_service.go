package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"tenant-service/internal/domain"
	"tenant-service/internal/httputil"
	"tenant-service/internal/repository"
)

var (
	ErrInvalidPlan    = errors.New("workspace service: invalid plan, must be shared or dedicated")
	ErrTenantNotFound = errors.New("workspace service: tenant not found")
)

var requiredServices = []string{"order-service"}

type RegisterWorkspaceInput struct {
	OwnerEmail string
	OwnerName  string
	Plan       string // "shared" | "dedicated"
	TenantName string
}

type RegisterWorkspaceOutput struct {
	TenantID string
	Status   string
}

type InfraUpdateInput struct {
	TenantID    string
	ServiceName string
	DBHost      string
	DBPort      int
	DBName      string
	DBUser      string
	SchemaName  string
}

type RoutingOutput struct {
	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	SchemaName string
}

// ControlPlaneRepository is the consumer-side interface expected by WorkspaceService.
type ControlPlaneRepository interface {
	CreateTenant(ctx context.Context, input repository.CreateTenantInput) error
	UpsertServiceInfrastructure(ctx context.Context, input repository.UpsertServiceInfraInput) error
	GetPendingServiceCount(ctx context.Context, tenantID string, requiredServices []string) (int, error)
	GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error)
	ActivateTenant(ctx context.Context, tenantID string) error
	GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*domain.TenantInfra, error)
}

// OutboxRepository is the consumer-side interface expected by WorkspaceService.
type OutboxRepository interface {
	CreateOutboxMessage(ctx context.Context, msg domain.OutboxMessage) error
}

// OutboxWorker is the consumer-side interface expected by WorkspaceService.
type OutboxWorker interface {
	Poke()
}

type WorkspaceService struct {
	controlPlaneRepository ControlPlaneRepository
	outboxRepository       OutboxRepository
	outboxWorker           OutboxWorker
}

type WorkspaceServiceParams struct {
	ControlPlaneRepository ControlPlaneRepository
	OutboxRepository       OutboxRepository
	OutboxWorker           OutboxWorker
}

func NewWorkspaceService(params WorkspaceServiceParams) *WorkspaceService {
	return &WorkspaceService{
		controlPlaneRepository: params.ControlPlaneRepository,
		outboxRepository:       params.OutboxRepository,
		outboxWorker:           params.OutboxWorker,
	}
}

func (s *WorkspaceService) RegisterWorkspace(ctx context.Context, input RegisterWorkspaceInput) (*RegisterWorkspaceOutput, error) {
	plan := domain.Plan(strings.ToLower(input.Plan))
	if !plan.IsValid() {
		return nil, fmt.Errorf("invalid plan '%s': must be '%s' or '%s'", input.Plan, domain.PlanShared, domain.PlanDedicated)
	}

	tenantID := domain.GenerateTenantID()
	slug := httputil.SanitizeSlug(input.TenantName)
	outboxID := domain.GenerateOutboxID()

	evt := domain.WorkspaceInitiatedEvent{
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

	if err := s.controlPlaneRepository.CreateTenant(ctx, repository.CreateTenantInput{
		ID:         tenantID,
		Name:       input.TenantName,
		Slug:       slug,
		OwnerEmail: input.OwnerEmail,
		OwnerName:  input.OwnerName,
		Plan:       plan.String(),
	}); err != nil {
		return nil, fmt.Errorf("failed to create tenant record: %w", err)
	}

	outboxMsg := domain.OutboxMessage{
		ID:            outboxID,
		TenantID:      &tenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   tenantID,
		EventType:     "workspace.initiated",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepository.CreateOutboxMessage(ctx, outboxMsg); err != nil {
		return nil, fmt.Errorf("failed to stage workspace.initiated outbox event: %w", err)
	}

	log.Printf("WorkspaceService: Registered tenant_id='%s' slug='%s' plan='%s' outbox_id='%s'",
		tenantID, slug, plan, outboxID)

	s.outboxWorker.Poke()
	return &RegisterWorkspaceOutput{TenantID: tenantID}, nil
}

func (s *WorkspaceService) HandleInfrastructureUpdate(ctx context.Context, input InfraUpdateInput) error {
	if err := s.controlPlaneRepository.UpsertServiceInfrastructure(ctx, repository.UpsertServiceInfraInput{
		TenantID:    input.TenantID,
		ServiceName: input.ServiceName,
		DBHost:      input.DBHost,
		DBPort:      input.DBPort,
		DBName:      input.DBName,
		DBUser:      input.DBUser,
		SchemaName:  input.SchemaName,
	}); err != nil {
		return fmt.Errorf("failed to upsert service infrastructure: %w", err)
	}

	log.Printf("WorkspaceService: Infrastructure routing updated for tenant_id='%s' service='%s' host='%s'",
		input.TenantID, input.ServiceName, input.DBHost)

	pendingCount, err := s.controlPlaneRepository.GetPendingServiceCount(ctx, input.TenantID, requiredServices)
	if err != nil {
		return fmt.Errorf("failed to check pending service count: %w", err)
	}

	if pendingCount > 0 {
		log.Printf("WorkspaceService: %d required service(s) still pending for tenant_id='%s'", pendingCount, input.TenantID)
		return nil
	}

	tenant, err := s.controlPlaneRepository.GetTenantByID(ctx, input.TenantID)
	if err != nil {
		return fmt.Errorf("failed to fetch tenant for activation: %w", err)
	}

	outboxID := domain.GenerateOutboxID()
	readyEvt := domain.WorkspaceReadyEvent{
		EventID:    outboxID,
		TenantID:   input.TenantID,
		OwnerEmail: tenant.OwnerEmail,
	}
	payloadBytes, err := json.Marshal(readyEvt)
	if err != nil {
		return fmt.Errorf("failed to marshal WorkspaceReady event: %w", err)
	}

	if err := s.controlPlaneRepository.ActivateTenant(ctx, input.TenantID); err != nil {
		return fmt.Errorf("failed to activate tenant: %w", err)
	}

	outboxMsg := domain.OutboxMessage{
		ID:            outboxID,
		TenantID:      &input.TenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   input.TenantID,
		EventType:     "workspace.ready",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepository.CreateOutboxMessage(ctx, outboxMsg); err != nil {
		return fmt.Errorf("failed to stage workspace.ready outbox event: %w", err)
	}

	log.Printf("WorkspaceService: Workspace ACTIVE for tenant_id='%s' — WorkspaceReady staged.", input.TenantID)
	s.outboxWorker.Poke()
	return nil
}

func (s *WorkspaceService) GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*RoutingOutput, error) {
	infra, err := s.controlPlaneRepository.GetServiceInfrastructure(ctx, tenantID, serviceName)
	if err != nil {
		return nil, err
	}
	return &RoutingOutput{
		DBHost:     infra.DBHost,
		DBPort:     infra.DBPort,
		DBName:     infra.DBName,
		DBUser:     infra.DBUser,
		SchemaName: infra.SchemaName,
	}, nil
}
