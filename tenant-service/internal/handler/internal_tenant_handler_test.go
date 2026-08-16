package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"tenant-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockInternalTenantInfraService struct {
	getServiceInfrastructureFn func(ctx context.Context, tenantID, serviceName string) (*service.RoutingOutput, error)
}

func (m *mockInternalTenantInfraService) GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*service.RoutingOutput, error) {
	if m.getServiceInfrastructureFn != nil {
		return m.getServiceInfrastructureFn(ctx, tenantID, serviceName)
	}
	return nil, nil
}

func setupInternalTenantTestRouter(internalTenantHandler *InternalTenantHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/internal/tenants/:tenant_id/infrastructure/:service_name", internalTenantHandler.GetServiceInfrastructure)
	return router
}

func TestNewInternalTenantHandler(t *testing.T) {
	mockInternalTenantInfrastructureService := &mockInternalTenantInfraService{}
	internalTenantHandler := NewInternalTenantHandler(mockInternalTenantInfrastructureService)
	if internalTenantHandler == nil || internalTenantHandler.tenantInfraService != mockInternalTenantInfrastructureService {
		t.Fatal("expected NewInternalTenantHandler to return non-nil pointer with service initialized")
	}
}

func TestInternalTenantHandler_GetServiceInfrastructure_Success(t *testing.T) {
	mockInternalTenantInfrastructureService := &mockInternalTenantInfraService{
		getServiceInfrastructureFn: func(ctx context.Context, tenantID, serviceName string) (*service.RoutingOutput, error) {
			return &service.RoutingOutput{
				DBHost:     "localhost",
				DBPort:     5432,
				DBName:     "order_db",
				DBUser:     "postgres",
				SchemaName: "tenant_100",
			}, nil
		},
	}

	internalTenantHandler := NewInternalTenantHandler(mockInternalTenantInfrastructureService)
	router := setupInternalTenantTestRouter(internalTenantHandler)

	req, _ := http.NewRequest(http.MethodGet, "/internal/tenants/ten_100/infrastructure/order-service", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected HTTP 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestInternalTenantHandler_GetServiceInfrastructure_ServiceError(t *testing.T) {
	mockInternalTenantInfrastructureService := &mockInternalTenantInfraService{
		getServiceInfrastructureFn: func(ctx context.Context, tenantID, serviceName string) (*service.RoutingOutput, error) {
			return nil, errors.New("infrastructure not found")
		},
	}

	internalTenantHandler := NewInternalTenantHandler(mockInternalTenantInfrastructureService)
	router := setupInternalTenantTestRouter(internalTenantHandler)

	req, _ := http.NewRequest(http.MethodGet, "/internal/tenants/ten_404/infrastructure/order-service", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected HTTP 404 Not Found, got %d. Body: %s", w.Code, w.Body.String())
	}
}
