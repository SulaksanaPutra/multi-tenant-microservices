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
	ErrInvalidPlan        = errors.New("workspace service: invalid plan, must be shared or dedicated")
	ErrTenantNotFound     = errors.New("workspace service: tenant not found")
	ErrOwnerEmailRequired = errors.New("workspace service: owner_email is required")
	ErrTenantNameRequired = errors.New("workspace service: tenant_name is required")
	ErrTenantIDRequired   = errors.New("workspace service: tenant_id is required")
)

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

// TenantRepository is the consumer-side interface expected by WorkspaceService.
type TenantRepository interface {
	CreateTenant(ctx context.Context, input repository.CreateTenantInput) error
	GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error)
	ActivateTenant(ctx context.Context, tenantID string) error
}

// OutboxRepository is the consumer-side interface expected by WorkspaceService.
type OutboxRepository interface {
	CreateOutboxMessage(ctx context.Context, input repository.CreateOutboxMessageInput) error
}

// OutboxWorker is the consumer-side interface expected by WorkspaceService.
type OutboxWorker interface {
	Poke()
}

type WorkspaceService struct {
	tenantRepository TenantRepository
	outboxRepository OutboxRepository
	outboxWorker     OutboxWorker
}

type WorkspaceServiceParams struct {
	TenantRepository TenantRepository
	OutboxRepository OutboxRepository
	OutboxWorker     OutboxWorker
}

func NewWorkspaceService(params WorkspaceServiceParams) *WorkspaceService {
	return &WorkspaceService{
		tenantRepository: params.TenantRepository,
		outboxRepository: params.OutboxRepository,
		outboxWorker:     params.OutboxWorker,
	}
}

func (s *WorkspaceService) RegisterWorkspace(ctx context.Context, input RegisterWorkspaceInput) (*RegisterWorkspaceOutput, error) {
	if strings.TrimSpace(input.OwnerEmail) == "" {
		return nil, ErrOwnerEmailRequired
	}
	if strings.TrimSpace(input.TenantName) == "" {
		return nil, ErrTenantNameRequired
	}

	plan := domain.Plan(strings.ToLower(input.Plan))
	if !plan.IsValid() {
		return nil, fmt.Errorf("%w: '%s' (must be '%s' or '%s')", ErrInvalidPlan, input.Plan, domain.PlanShared, domain.PlanDedicated)
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

	if err := s.tenantRepository.CreateTenant(ctx, repository.CreateTenantInput{
		ID:         tenantID,
		Name:       input.TenantName,
		Slug:       slug,
		OwnerEmail: input.OwnerEmail,
		OwnerName:  input.OwnerName,
		Plan:       plan.String(),
	}); err != nil {
		return nil, fmt.Errorf("failed to create tenant record: %w", err)
	}

	outboxInput := repository.CreateOutboxMessageInput{
		ID:            outboxID,
		TenantID:      tenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   tenantID,
		EventType:     "workspace.initiated",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepository.CreateOutboxMessage(ctx, outboxInput); err != nil {
		return nil, fmt.Errorf("failed to stage workspace.initiated outbox event: %w", err)
	}

	log.Printf("WorkspaceService: Registered tenant_id='%s' slug='%s' plan='%s' outbox_id='%s'",
		tenantID, slug, plan, outboxID)

	if s.outboxWorker != nil {
		s.outboxWorker.Poke()
	}
	return &RegisterWorkspaceOutput{TenantID: tenantID, Status: "accepted"}, nil
}

func (s *WorkspaceService) ActivateWorkspace(ctx context.Context, tenantID string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantIDRequired
	}

	tenant, err := s.tenantRepository.GetTenantByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrTenantNotFound, tenantID)
		}
		return fmt.Errorf("failed to fetch tenant for activation: %w", err)
	}
	if tenant == nil {
		return fmt.Errorf("%w: %s", ErrTenantNotFound, tenantID)
	}

	outboxID := domain.GenerateOutboxID()
	readyEvt := domain.WorkspaceReadyEvent{
		EventID:    outboxID,
		TenantID:   tenantID,
		OwnerEmail: tenant.OwnerEmail,
	}
	payloadBytes, err := json.Marshal(readyEvt)
	if err != nil {
		return fmt.Errorf("failed to marshal WorkspaceReady event: %w", err)
	}

	if err := s.tenantRepository.ActivateTenant(ctx, tenantID); err != nil {
		return fmt.Errorf("failed to activate tenant: %w", err)
	}

	outboxInput := repository.CreateOutboxMessageInput{
		ID:            outboxID,
		TenantID:      tenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   tenantID,
		EventType:     "workspace.ready",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepository.CreateOutboxMessage(ctx, outboxInput); err != nil {
		return fmt.Errorf("failed to stage workspace.ready outbox event: %w", err)
	}

	log.Printf("WorkspaceService: Workspace ACTIVE for tenant_id='%s' — WorkspaceReady staged.", tenantID)
	if s.outboxWorker != nil {
		s.outboxWorker.Poke()
	}
	return nil
}
