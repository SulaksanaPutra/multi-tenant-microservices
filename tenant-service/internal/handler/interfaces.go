package handler

import (
	"context"

	"tenant-service/internal/service"
)

// =============================================================================
// Transaction Infrastructure Contracts
// =============================================================================

// TxManager is the handler-side interface for managing atomic unit-of-work transactions.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

// =============================================================================
// Public Domain Service Contracts
// =============================================================================

// TenantServiceInterface is the handler-side interface for tenant lifecycle operations.
type TenantServiceInterface interface {
	GetTenantByID(ctx context.Context, tenantID string) (*service.TenantOutput, error)
	UpdateTenant(ctx context.Context, input service.UpdateTenantServiceInput) error
	ChangeTenantPlan(ctx context.Context, input service.ChangeTenantPlanInput) error
}

// WorkspaceService is the handler-side interface for workspace registration workflows.
type WorkspaceService interface {
	RegisterWorkspace(ctx context.Context, input service.RegisterWorkspaceInput) (*service.RegisterWorkspaceOutput, error)
}

// =============================================================================
// Internal Inter-Service Contracts
// =============================================================================

// InternalTenantInfrastructureService is the handler-side interface for routing & infra lookups.
type InternalTenantInfrastructureService interface {
	GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*service.RoutingOutput, error)
}
