package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"tenant-service/internal/domain"
	"tenant-service/internal/repository"
)

type mockInfrastructureRepository struct {
	upsertFunc          func(ctx context.Context, input repository.UpsertServiceInfrastructureInput) error
	getPendingCountFunc func(ctx context.Context, tenantID string, requiredServices []string) (int, error)
	getInfraFunc        func(ctx context.Context, tenantID, serviceName string) (*domain.TenantInfra, error)
}

func (m *mockInfrastructureRepository) UpsertServiceInfrastructure(ctx context.Context, input repository.UpsertServiceInfrastructureInput) error {
	if m.upsertFunc != nil {
		return m.upsertFunc(ctx, input)
	}
	return nil
}

func (m *mockInfrastructureRepository) CountPendingServices(ctx context.Context, tenantID string, requiredServices []string) (int, error) {
	if m.getPendingCountFunc != nil {
		return m.getPendingCountFunc(ctx, tenantID, requiredServices)
	}
	return 0, nil
}

func (m *mockInfrastructureRepository) FindByServiceName(ctx context.Context, tenantID, serviceName string) (*domain.TenantInfra, error) {
	if m.getInfraFunc != nil {
		return m.getInfraFunc(ctx, tenantID, serviceName)
	}
	return &domain.TenantInfra{
		TenantID:    tenantID,
		ServiceName: serviceName,
		DBHost:      "localhost",
		DBPort:      5432,
		DBName:      "tenant_db",
		DBUser:      "tenant_user",
		SchemaName:  "public",
	}, nil
}

type mockWorkspaceActivator struct {
	activatedTenantID string
	activateFunc      func(ctx context.Context, tenantID string) error
}

func (m *mockWorkspaceActivator) ActivateWorkspace(ctx context.Context, tenantID string) error {
	m.activatedTenantID = tenantID
	if m.activateFunc != nil {
		return m.activateFunc(ctx, tenantID)
	}
	return nil
}

func TestTenantInfrastructureService_HandleUpdate_Validation(t *testing.T) {
	tenantInfrastructureService := NewTenantInfrastructureService(TenantInfrastructureServiceParams{
		InfrastructureRepository: &mockInfrastructureRepository{},
	})

	t.Run("missing tenant_id", func(t *testing.T) {
		err := tenantInfrastructureService.HandleInfrastructureUpdate(context.Background(), InfrastructureUpdateInput{
			ServiceName: "order-service",
		})
		if !errors.Is(err, domain.ErrTenantIDRequired) {
			t.Errorf("expected ErrTenantIDRequired, got %v", err)
		}
	})

	t.Run("missing service_name", func(t *testing.T) {
		err := tenantInfrastructureService.HandleInfrastructureUpdate(context.Background(), InfrastructureUpdateInput{
			TenantID: "tenant-1",
		})
		if !errors.Is(err, domain.ErrServiceNameRequired) {
			t.Errorf("expected ErrServiceNameRequired, got %v", err)
		}
	})
}

func TestTenantInfrastructureService_HandleUpdate_PendingServices(t *testing.T) {
	mockWorkspaceActivator := &mockWorkspaceActivator{}
	mockInfrastructureRepository := &mockInfrastructureRepository{
		getPendingCountFunc: func(ctx context.Context, tenantID string, requiredServices []string) (int, error) {
			return 1, nil // 1 service still pending
		},
	}

	tenantInfrastructureService := NewTenantInfrastructureService(TenantInfrastructureServiceParams{
		InfrastructureRepository: mockInfrastructureRepository,
		WorkspaceActivator:       mockWorkspaceActivator,
	})

	err := tenantInfrastructureService.HandleInfrastructureUpdate(context.Background(), InfrastructureUpdateInput{
		TenantID:    "t-pending",
		ServiceName: "order-service",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockWorkspaceActivator.activatedTenantID != "" {
		t.Errorf("expected workspace NOT to be activated while services are pending, got activated for '%s'", mockWorkspaceActivator.activatedTenantID)
	}
}

func TestTenantInfrastructureService_HandleUpdate_AllServicesReady(t *testing.T) {
	mockWorkspaceActivator := &mockWorkspaceActivator{}
	mockInfrastructureRepository := &mockInfrastructureRepository{
		getPendingCountFunc: func(ctx context.Context, tenantID string, requiredServices []string) (int, error) {
			return 0, nil // 0 pending -> barrier satisfied
		},
	}

	tenantInfrastructureService := NewTenantInfrastructureService(TenantInfrastructureServiceParams{
		InfrastructureRepository: mockInfrastructureRepository,
		WorkspaceActivator:       mockWorkspaceActivator,
	})

	err := tenantInfrastructureService.HandleInfrastructureUpdate(context.Background(), InfrastructureUpdateInput{
		TenantID:    "t-ready",
		ServiceName: "order-service",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockWorkspaceActivator.activatedTenantID != "t-ready" {
		t.Errorf("expected workspace activation for 't-ready', got '%s'", mockWorkspaceActivator.activatedTenantID)
	}
}

func TestTenantInfrastructureService_DynamicRequiredServices(t *testing.T) {
	customServices := []string{"order-service", "inventory-service"}
	var passedServices []string

	mockInfrastructureRepository := &mockInfrastructureRepository{
		getPendingCountFunc: func(ctx context.Context, tenantID string, requiredServices []string) (int, error) {
			passedServices = requiredServices
			return 0, nil
		},
	}

	tenantInfrastructureService := NewTenantInfrastructureService(TenantInfrastructureServiceParams{
		InfrastructureRepository: mockInfrastructureRepository,
		RequiredServices:         customServices,
	})

	err := tenantInfrastructureService.HandleInfrastructureUpdate(context.Background(), InfrastructureUpdateInput{
		TenantID:    "t-multi",
		ServiceName: "order-service",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(passedServices, customServices) {
		t.Errorf("expected requiredServices %v to be passed to repo, got %v", customServices, passedServices)
	}
}

func TestTenantInfrastructureService_GetServiceInfrastructure(t *testing.T) {
	tenantInfrastructureService := NewTenantInfrastructureService(TenantInfrastructureServiceParams{
		InfrastructureRepository: &mockInfrastructureRepository{},
	})

	t.Run("missing parameters validation", func(t *testing.T) {
		_, err := tenantInfrastructureService.GetServiceInfrastructure(context.Background(), "", "order-service")
		if !errors.Is(err, domain.ErrTenantIDRequired) {
			t.Errorf("expected ErrTenantIDRequired, got %v", err)
		}

		_, err = tenantInfrastructureService.GetServiceInfrastructure(context.Background(), "t-1", "")
		if !errors.Is(err, domain.ErrServiceNameRequired) {
			t.Errorf("expected ErrServiceNameRequired, got %v", err)
		}
	})

	t.Run("happy path returning routing output", func(t *testing.T) {
		output, err := tenantInfrastructureService.GetServiceInfrastructure(context.Background(), "t-1", "order-service")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if output.DBHost != "localhost" || output.DBPort != 5432 {
			t.Errorf("unexpected routing output: %+v", output)
		}
	})
}
