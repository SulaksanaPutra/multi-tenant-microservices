package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"tenant-service/internal/domain"
	"tenant-service/internal/httputil"
	"tenant-service/internal/repository"
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

type UpdateTenantServiceInput struct {
	TenantID   string
	Name       string
	Slug       string
	OwnerEmail *string
	OwnerName  *string
}

type ChangeTenantPlanInput struct {
	TenantID string
	Plan     string
}

type TenantOutput struct {
	ID         string
	Name       string
	Slug       string
	OwnerEmail string
	OwnerName  string
	Plan       string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func toTenantOutput(t domain.Tenant) TenantOutput {
	return TenantOutput{
		ID:         t.ID,
		Name:       t.Name,
		Slug:       t.Slug,
		OwnerEmail: t.OwnerEmail,
		OwnerName:  t.OwnerName,
		Plan:       t.Plan,
		Status:     t.Status,
		CreatedAt:  t.CreatedAt,
		UpdatedAt:  t.UpdatedAt,
	}
}

// TenantRepository is the consumer-side interface expected by WorkspaceService.
type TenantRepository interface {
	CreateTenant(ctx context.Context, input repository.CreateTenantInput) error
	GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error)
	ActivateTenant(ctx context.Context, tenantID string) error
	UpdateTenant(ctx context.Context, input repository.UpdateTenantInput) error
	UpdateTenantPlan(ctx context.Context, input repository.UpdateTenantPlanInput) error
	SetTenantStatus(ctx context.Context, tenantID, status string) error
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

func (workspaceService *WorkspaceService) RegisterWorkspace(ctx context.Context, input RegisterWorkspaceInput) (*RegisterWorkspaceOutput, error) {
	if strings.TrimSpace(input.OwnerEmail) == "" {
		return nil, domain.ErrOwnerEmailRequired
	}
	if strings.TrimSpace(input.TenantName) == "" {
		return nil, domain.ErrTenantNameRequired
	}

	plan := domain.Plan(strings.ToLower(input.Plan))
	if !plan.IsValid() {
		return nil, fmt.Errorf("%w: '%s' (must be '%s' or '%s')", domain.ErrInvalidPlan, input.Plan, domain.PlanShared, domain.PlanDedicated)
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
		return nil, fmt.Errorf("workspace service: failed to marshal WorkspaceInitiated event: %w", err)
	}

	if err := workspaceService.tenantRepository.CreateTenant(ctx, repository.CreateTenantInput{
		ID:         tenantID,
		Name:       input.TenantName,
		Slug:       slug,
		OwnerEmail: input.OwnerEmail,
		OwnerName:  input.OwnerName,
		Plan:       plan.String(),
	}); err != nil {
		return nil, fmt.Errorf("workspace service: failed to create tenant record: %w", err)
	}

	outboxInput := repository.CreateOutboxMessageInput{
		ID:            outboxID,
		TenantID:      tenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   tenantID,
		EventType:     "workspace.initiated",
		Payload:       payloadBytes,
	}
	if err := workspaceService.outboxRepository.CreateOutboxMessage(ctx, outboxInput); err != nil {
		return nil, fmt.Errorf("workspace service: failed to stage workspace.initiated outbox event: %w", err)
	}

	log.Printf("WorkspaceService: Registered tenant_id='%s' slug='%s' plan='%s' outbox_id='%s'",
		tenantID, slug, plan, outboxID)

	if workspaceService.outboxWorker != nil {
		workspaceService.outboxWorker.Poke()
	}
	return &RegisterWorkspaceOutput{TenantID: tenantID, Status: "accepted"}, nil
}

func (workspaceService *WorkspaceService) ActivateWorkspace(ctx context.Context, tenantID string) error {
	if strings.TrimSpace(tenantID) == "" {
		return domain.ErrTenantIDRequired
	}

	tenant, err := workspaceService.tenantRepository.GetTenantByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: %s", domain.ErrTenantNotFound, tenantID)
		}
		return fmt.Errorf("workspace service: failed to fetch tenant for activation: %w", err)
	}
	if tenant == nil {
		return fmt.Errorf("%w: %s", domain.ErrTenantNotFound, tenantID)
	}

	outboxID := domain.GenerateOutboxID()
	readyEvt := domain.WorkspaceReadyEvent{
		EventID:    outboxID,
		TenantID:   tenantID,
		OwnerEmail: tenant.OwnerEmail,
		TenantName: tenant.Name,
		TenantSlug: tenant.Slug,
		OwnerName:  tenant.OwnerName,
	}
	payloadBytes, err := json.Marshal(readyEvt)
	if err != nil {
		return fmt.Errorf("workspace service: failed to marshal WorkspaceReady event: %w", err)
	}

	if err := workspaceService.tenantRepository.ActivateTenant(ctx, tenantID); err != nil {
		return fmt.Errorf("workspace service: failed to activate tenant: %w", err)
	}

	outboxInput := repository.CreateOutboxMessageInput{
		ID:            outboxID,
		TenantID:      tenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   tenantID,
		EventType:     "workspace.ready",
		Payload:       payloadBytes,
	}
	if err := workspaceService.outboxRepository.CreateOutboxMessage(ctx, outboxInput); err != nil {
		return fmt.Errorf("workspace service: failed to stage workspace.ready outbox event: %w", err)
	}

	// Stage tenant.infrastructure_changed broadcast outbox event to purge stale routing/connection pools across all microservices
	infraChangedID := domain.GenerateOutboxID()
	infraChangedEvt := domain.InfraChangedEvent{
		EventID:  infraChangedID,
		TenantID: tenantID,
	}
	icPayload, err := json.Marshal(infraChangedEvt)
	if err == nil {
		_ = workspaceService.outboxRepository.CreateOutboxMessage(ctx, repository.CreateOutboxMessageInput{
			ID:            infraChangedID,
			TenantID:      tenantID,
			AggregateType: "WORKSPACE",
			AggregateID:   tenantID,
			EventType:     domain.RoutingKeyInfraChanged,
			Payload:       icPayload,
		})
	}

	log.Printf("WorkspaceService: Workspace ACTIVE for tenant_id='%s' — WorkspaceReady & InfraChanged staged.", tenantID)
	if workspaceService.outboxWorker != nil {
		workspaceService.outboxWorker.Poke()
	}
	return nil
}

func (workspaceService *WorkspaceService) GetTenantByID(ctx context.Context, tenantID string) (*TenantOutput, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, domain.ErrTenantIDRequired
	}
	tenant, err := workspaceService.tenantRepository.GetTenantByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil {
		return nil, domain.ErrNotFound
	}
	output := toTenantOutput(*tenant)
	return &output, nil
}

// RollbackFailedMigration resets a tenant to ACTIVE after its infrastructure
// migration failed and stages the tenant.infrastructure_changed broadcast to
// unfreeze order-service replicas. It participates in the outer Unit-of-Work
// passed via txCtx when invoked from a consumer transaction.
func (workspaceService *WorkspaceService) RollbackFailedMigration(ctx context.Context, tenantID string) error {
	if strings.TrimSpace(tenantID) == "" {
		return domain.ErrTenantIDRequired
	}

	// 1. Reset tenant status back to ACTIVE
	if err := workspaceService.tenantRepository.SetTenantStatus(ctx, tenantID, domain.StatusActive); err != nil {
		return fmt.Errorf("workspace service: failed to set tenant status to active: %w", err)
	}

	// 2. Stage tenant.infrastructure_changed broadcast outbox message to unfreeze order-service replicas
	infraChangedID := domain.GenerateOutboxID()
	infraChangedEvt := domain.InfraChangedEvent{
		EventID:  infraChangedID,
		TenantID: tenantID,
	}
	icPayload, err := json.Marshal(infraChangedEvt)
	if err != nil {
		return fmt.Errorf("workspace service: failed to marshal InfraChanged payload: %w", err)
	}

	if err := workspaceService.outboxRepository.CreateOutboxMessage(ctx, repository.CreateOutboxMessageInput{
		ID:            infraChangedID,
		TenantID:      tenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   tenantID,
		EventType:     domain.RoutingKeyInfraChanged,
		Payload:       icPayload,
	}); err != nil {
		return fmt.Errorf("workspace service: failed to stage InfraChanged event: %w", err)
	}

	return nil
}

func (workspaceService *WorkspaceService) UpdateTenant(ctx context.Context, input UpdateTenantServiceInput) error {
	if strings.TrimSpace(input.TenantID) == "" {
		return domain.ErrTenantIDRequired
	}
	if strings.TrimSpace(input.Name) == "" {
		return domain.ErrTenantNameRequired
	}
	slug := input.Slug
	if slug == "" {
		slug = httputil.SanitizeSlug(input.Name)
	}

	return workspaceService.tenantRepository.UpdateTenant(ctx, repository.UpdateTenantInput{
		ID:         input.TenantID,
		Name:       input.Name,
		Slug:       slug,
		OwnerEmail: input.OwnerEmail,
		OwnerName:  input.OwnerName,
	})
}

func (workspaceService *WorkspaceService) ChangeTenantPlan(ctx context.Context, input ChangeTenantPlanInput) error {
	if strings.TrimSpace(input.TenantID) == "" {
		return domain.ErrTenantIDRequired
	}
	p := domain.Plan(strings.ToLower(input.Plan))
	if !p.IsValid() {
		return fmt.Errorf("%w: '%s' (must be '%s' or '%s')", domain.ErrInvalidPlan, input.Plan, domain.PlanShared, domain.PlanDedicated)
	}

	tenant, err := workspaceService.tenantRepository.GetTenantByID(ctx, input.TenantID)
	if err != nil {
		return fmt.Errorf("workspace service: failed to get tenant: %w", err)
	}

	if err := workspaceService.tenantRepository.UpdateTenantPlan(ctx, repository.UpdateTenantPlanInput{
		ID:   input.TenantID,
		Plan: p.String(),
	}); err != nil {
		return fmt.Errorf("workspace service: failed to update plan: %w", err)
	}

	if err := workspaceService.tenantRepository.SetTenantStatus(ctx, input.TenantID, domain.StatusMigrating); err != nil {
		return fmt.Errorf("workspace service: failed to set status to MIGRATING: %w", err)
	}

	// 1. Stage tenant.infrastructure_locking broadcast event
	lockEvtID := domain.GenerateOutboxID()
	lockEvt := domain.InfrastructureLockingEvent{
		EventID:  lockEvtID,
		TenantID: input.TenantID,
	}
	lockPayload, err := json.Marshal(lockEvt)
	if err != nil {
		return fmt.Errorf("workspace service: failed to marshal lock event: %w", err)
	}

	if err := workspaceService.outboxRepository.CreateOutboxMessage(ctx, repository.CreateOutboxMessageInput{
		ID:            lockEvtID,
		TenantID:      input.TenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   input.TenantID,
		EventType:     domain.RoutingKeyInfrastructureLocking,
		Payload:       lockPayload,
	}); err != nil {
		return fmt.Errorf("workspace service: failed to stage lock event: %w", err)
	}

	// 2. Stage workspace.initiated outbox event to trigger infra-provisioner
	initEvtID := domain.GenerateOutboxID()
	initEvt := domain.WorkspaceInitiatedEvent{
		EventID:    initEvtID,
		TenantID:   input.TenantID,
		Plan:       p.String(),
		OwnerEmail: tenant.OwnerEmail,
		OwnerName:  tenant.OwnerName,
	}
	initPayload, err := json.Marshal(initEvt)
	if err != nil {
		return fmt.Errorf("workspace service: failed to marshal workspace.initiated event: %w", err)
	}

	if err := workspaceService.outboxRepository.CreateOutboxMessage(ctx, repository.CreateOutboxMessageInput{
		ID:            initEvtID,
		TenantID:      input.TenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   input.TenantID,
		EventType:     domain.RoutingKeyWorkspaceInitiated,
		Payload:       initPayload,
	}); err != nil {
		return fmt.Errorf("workspace service: failed to stage workspace.initiated event: %w", err)
	}

	if workspaceService.outboxWorker != nil {
		workspaceService.outboxWorker.Poke()
	}

	return nil
}
