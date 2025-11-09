package service

import (
	"context"
	"fmt"
	"log"

	"tenant-service/internal/domain"
	"tenant-service/internal/repository"
)

var requiredServices = []string{"order-service"}

type InfrastructureUpdateInput struct {
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

// TenantInfrastructureRepository is the consumer-side interface expected by TenantInfrastructureService.
type TenantInfrastructureRepository interface {
	UpsertServiceInfrastructure(ctx context.Context, input repository.UpsertServiceInfrastructureInput) error
	GetPendingServiceCount(ctx context.Context, tenantID string, requiredServices []string) (int, error)
	GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*domain.TenantInfra, error)
}

// WorkspaceActivator is the consumer-side interface expected by TenantInfrastructureService to signal workspace activation.
type WorkspaceActivator interface {
	ActivateWorkspace(ctx context.Context, tenantID string) error
}

type TenantInfrastructureService struct {
	infrastructureRepository TenantInfrastructureRepository
	workspaceActivator       WorkspaceActivator
}

type TenantInfrastructureServiceParams struct {
	InfrastructureRepository TenantInfrastructureRepository
	WorkspaceActivator       WorkspaceActivator
}

func NewTenantInfrastructureService(params TenantInfrastructureServiceParams) *TenantInfrastructureService {
	return &TenantInfrastructureService{
		infrastructureRepository: params.InfrastructureRepository,
		workspaceActivator:       params.WorkspaceActivator,
	}
}

func (s *TenantInfrastructureService) HandleInfrastructureUpdate(ctx context.Context, input InfrastructureUpdateInput) error {
	if err := s.infrastructureRepository.UpsertServiceInfrastructure(ctx, repository.UpsertServiceInfrastructureInput{
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

	log.Printf("TenantInfrastructureService: Infrastructure routing updated for tenant_id='%s' service='%s' host='%s'",
		input.TenantID, input.ServiceName, input.DBHost)

	pendingCount, err := s.infrastructureRepository.GetPendingServiceCount(ctx, input.TenantID, requiredServices)
	if err != nil {
		return fmt.Errorf("failed to check pending service count: %w", err)
	}

	if pendingCount > 0 {
		log.Printf("TenantInfrastructureService: %d required service(s) still pending for tenant_id='%s'", pendingCount, input.TenantID)
		return nil
	}

	if err := s.workspaceActivator.ActivateWorkspace(ctx, input.TenantID); err != nil {
		return fmt.Errorf("failed to trigger workspace activation: %w", err)
	}

	return nil
}

func (s *TenantInfrastructureService) GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*RoutingOutput, error) {
	infra, err := s.infrastructureRepository.GetServiceInfrastructure(ctx, tenantID, serviceName)
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
