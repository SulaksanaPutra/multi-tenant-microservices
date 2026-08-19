package service

import (
	"context"
	"fmt"
	"log"
	"strings"

	"tenant-service/internal/domain"
	"tenant-service/internal/repository"
)

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

type TenantInfrastructureRepository interface {
	UpsertServiceInfrastructure(ctx context.Context, input repository.UpsertServiceInfrastructureInput) error
	CountPendingServices(ctx context.Context, tenantID string, requiredServices []string) (int, error)
	FindByServiceName(ctx context.Context, tenantID, serviceName string) (*domain.TenantInfra, error)
}

type WorkspaceActivator interface {
	ActivateWorkspace(ctx context.Context, tenantID string) error
}

type TenantInfrastructureService struct {
	infrastructureRepository TenantInfrastructureRepository
	workspaceActivator       WorkspaceActivator
	requiredServices         []string
}

type TenantInfrastructureServiceParams struct {
	InfrastructureRepository TenantInfrastructureRepository
	WorkspaceActivator       WorkspaceActivator
	RequiredServices         []string
}

func NewTenantInfrastructureService(params TenantInfrastructureServiceParams) *TenantInfrastructureService {
	reqServices := params.RequiredServices
	if len(reqServices) == 0 {
		reqServices = []string{"order-service"}
	}
	return &TenantInfrastructureService{
		infrastructureRepository: params.InfrastructureRepository,
		workspaceActivator:       params.WorkspaceActivator,
		requiredServices:         reqServices,
	}
}

func (tenantInfrastructureService *TenantInfrastructureService) HandleInfrastructureUpdate(ctx context.Context, input InfrastructureUpdateInput) error {
	if strings.TrimSpace(input.TenantID) == "" {
		return domain.ErrTenantIDRequired
	}
	if strings.TrimSpace(input.ServiceName) == "" {
		return domain.ErrServiceNameRequired
	}

	if err := tenantInfrastructureService.infrastructureRepository.UpsertServiceInfrastructure(ctx, repository.UpsertServiceInfrastructureInput{
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

	pendingCount, err := tenantInfrastructureService.infrastructureRepository.CountPendingServices(ctx, input.TenantID, tenantInfrastructureService.requiredServices)
	if err != nil {
		return fmt.Errorf("failed to check pending service count: %w", err)
	}

	if pendingCount > 0 {
		log.Printf("TenantInfrastructureService: %d required service(s) still pending for tenant_id='%s'", pendingCount, input.TenantID)
		return nil
	}

	if tenantInfrastructureService.workspaceActivator != nil {
		if err := tenantInfrastructureService.workspaceActivator.ActivateWorkspace(ctx, input.TenantID); err != nil {
			return fmt.Errorf("failed to trigger workspace activation: %w", err)
		}
	}

	return nil
}

func (tenantInfrastructureService *TenantInfrastructureService) GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*RoutingOutput, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, domain.ErrTenantIDRequired
	}
	if strings.TrimSpace(serviceName) == "" {
		return nil, domain.ErrServiceNameRequired
	}

	infra, err := tenantInfrastructureService.infrastructureRepository.FindByServiceName(ctx, tenantID, serviceName)
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
