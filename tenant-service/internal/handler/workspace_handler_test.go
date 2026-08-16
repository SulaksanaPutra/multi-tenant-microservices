package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tenant-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockTxManager struct {
	withTransactionFn func(ctx context.Context, fn func(txCtx context.Context) error) error
}

func (m *mockTxManager) WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	if m.withTransactionFn != nil {
		return m.withTransactionFn(ctx, fn)
	}
	return fn(ctx)
}

type mockWorkspaceService struct {
	registerWorkspaceFn func(ctx context.Context, input service.RegisterWorkspaceInput) (*service.RegisterWorkspaceOutput, error)
}

func (m *mockWorkspaceService) RegisterWorkspace(ctx context.Context, input service.RegisterWorkspaceInput) (*service.RegisterWorkspaceOutput, error) {
	if m.registerWorkspaceFn != nil {
		return m.registerWorkspaceFn(ctx, input)
	}
	return &service.RegisterWorkspaceOutput{Status: "accepted"}, nil
}

type mockTenantInfrastructureService struct {
	getServiceInfrastructureFn func(ctx context.Context, tenantID, serviceName string) (*service.RoutingOutput, error)
}

func (m *mockTenantInfrastructureService) GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*service.RoutingOutput, error) {
	if m.getServiceInfrastructureFn != nil {
		return m.getServiceInfrastructureFn(ctx, tenantID, serviceName)
	}
	return &service.RoutingOutput{
		DBHost:     "localhost",
		DBPort:     5432,
		DBName:     "test_db",
		DBUser:     "test_user",
		SchemaName: "public",
	}, nil
}

func TestWorkspaceHandler_Constructor(t *testing.T) {
	workspaceHandler := NewWorkspaceHandler(&mockTxManager{}, &mockWorkspaceService{})
	if workspaceHandler == nil {
		t.Fatal("expected NewWorkspaceHandler to return a non-nil struct pointer")
	}
}

func TestRegisterWorkspace_ValidationError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	workspaceHandler := NewWorkspaceHandler(&mockTxManager{}, &mockWorkspaceService{})

	router := gin.New()
	router.POST("/workspaces", workspaceHandler.RegisterWorkspace)

	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "missing all fields",
			body: map[string]any{},
		},
		{
			name: "invalid owner_email",
			body: map[string]any{
				"owner_email": "not-an-email",
				"owner_name":  "Alice",
				"plan":        "shared",
				"tenant_name": "Acme",
			},
		},
		{
			name: "invalid plan option",
			body: map[string]any{
				"owner_email": "alice@acme.com",
				"owner_name":  "Alice",
				"plan":        "unsupported_plan",
				"tenant_name": "Acme",
			},
		},
		{
			name: "missing tenant_name",
			body: map[string]any{
				"owner_email": "alice@acme.com",
				"owner_name":  "Alice",
				"plan":        "dedicated",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBytes, _ := json.Marshal(tt.body)
			req, _ := http.NewRequest(http.MethodPost, "/workspaces", bytes.NewBuffer(jsonBytes))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status 400 Bad Request, got %d. Body: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRegisterWorkspace_ServiceError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	serviceErr := errors.New("registration processing failed")
	workspaceService := &mockWorkspaceService{
		registerWorkspaceFn: func(ctx context.Context, input service.RegisterWorkspaceInput) (*service.RegisterWorkspaceOutput, error) {
			return nil, serviceErr
		},
	}

	workspaceHandler := NewWorkspaceHandler(&mockTxManager{}, workspaceService)
	router := gin.New()
	router.POST("/workspaces", workspaceHandler.RegisterWorkspace)

	body := map[string]any{
		"owner_email": "alice@acme.com",
		"owner_name":  "Alice",
		"plan":        "dedicated",
		"tenant_name": "Acme Corp",
	}
	jsonBytes, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, "/workspaces", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 Internal Server Error, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "registration processing failed") {
		t.Errorf("expected body to contain error message, got: %s", w.Body.String())
	}
}

func TestRegisterWorkspace_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var capturedInput service.RegisterWorkspaceInput
	workspaceService := &mockWorkspaceService{
		registerWorkspaceFn: func(ctx context.Context, input service.RegisterWorkspaceInput) (*service.RegisterWorkspaceOutput, error) {
			capturedInput = input
			return &service.RegisterWorkspaceOutput{Status: "accepted"}, nil
		},
	}

	workspaceHandler := NewWorkspaceHandler(&mockTxManager{}, workspaceService)
	router := gin.New()
	router.POST("/workspaces", workspaceHandler.RegisterWorkspace)

	body := map[string]any{
		"owner_email": "owner@acme.com",
		"owner_name":  "Owner Name",
		"plan":        "shared",
		"tenant_name": "Acme Inc",
	}
	jsonBytes, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, "/workspaces", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected status 202 Accepted, got %d. Body: %s", w.Code, w.Body.String())
	}

	if capturedInput.OwnerEmail != "owner@acme.com" || capturedInput.OwnerName != "Owner Name" ||
		capturedInput.Plan != "shared" || capturedInput.TenantName != "Acme Inc" {
		t.Errorf("unexpected input captured by service: %+v", capturedInput)
	}
}

func TestInternalTenantHandler_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	internalTenantHandler := NewInternalTenantHandler(&mockTenantInfrastructureService{})

	router := gin.New()
	router.GET("/infrastructure", internalTenantHandler.GetServiceInfrastructure)

	req, _ := http.NewRequest(http.MethodGet, "/infrastructure", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 Bad Request when path parameters are missing, got %d", w.Code)
	}
}

func TestInternalTenantHandler_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var capturedTenantID, capturedServiceName string
	tenantInfrastructureService := &mockTenantInfrastructureService{
		getServiceInfrastructureFn: func(ctx context.Context, tenantID, serviceName string) (*service.RoutingOutput, error) {
			capturedTenantID = tenantID
			capturedServiceName = serviceName
			return &service.RoutingOutput{
				DBHost:     "pg-host-1",
				DBPort:     5433,
				DBName:     "order_db_100",
				DBUser:     "user_100",
				SchemaName: "tenant_schema_100",
			}, nil
		},
	}

	internalTenantHandler := NewInternalTenantHandler(tenantInfrastructureService)
	router := gin.New()
	router.GET("/tenants/:tenant_id/infrastructure/:service_name", internalTenantHandler.GetServiceInfrastructure)

	req, _ := http.NewRequest(http.MethodGet, "/tenants/t-100/infrastructure/order-service", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	if capturedTenantID != "t-100" || capturedServiceName != "order-service" {
		t.Errorf("unexpected parameters captured: tenantID=%s, serviceName=%s", capturedTenantID, capturedServiceName)
	}
}
