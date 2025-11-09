package handler

import (
	"context"
	"net/http"

	"tenant-service/internal/httputil"
	"tenant-service/internal/service"

	"github.com/gin-gonic/gin"
)

type RegisterWorkspaceRequest struct {
	OwnerEmail string `json:"owner_email" binding:"required,email"`
	OwnerName  string `json:"owner_name"  binding:"required"`
	Plan       string `json:"plan"        binding:"required,oneof=shared dedicated"`
	TenantName string `json:"tenant_name" binding:"required"`
}

type RegisterWorkspaceResponse struct {
	TenantID string `json:"tenant_id"`
}

type GetInfrastructureResponse struct {
	DBHost     string `json:"db_host"`
	DBPort     int    `json:"db_port"`
	DBName     string `json:"db_name"`
	DBUser     string `json:"db_user"`
	SchemaName string `json:"schema_name"`
}

// TxManager is the consumer-side interface expected by WorkspaceHandler.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

// WorkspaceService is the consumer-side interface expected by WorkspaceHandler.
type WorkspaceService interface {
	RegisterWorkspace(ctx context.Context, input service.RegisterWorkspaceInput) (*service.RegisterWorkspaceOutput, error)
}

// TenantInfrastructureService is the consumer-side interface expected by WorkspaceHandler.
type TenantInfrastructureService interface {
	GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*service.RoutingOutput, error)
}

type WorkspaceHandler struct {
	txManager                   TxManager
	workspaceService            WorkspaceService
	tenantInfrastructureService TenantInfrastructureService
}

func NewWorkspaceHandler(txManager TxManager, workspaceService WorkspaceService, tenantInfrastructureService TenantInfrastructureService) *WorkspaceHandler {
	return &WorkspaceHandler{
		txManager:                   txManager,
		workspaceService:            workspaceService,
		tenantInfrastructureService: tenantInfrastructureService,
	}
}

func (h *WorkspaceHandler) RegisterWorkspace(c *gin.Context) {
	var req RegisterWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	var output *service.RegisterWorkspaceOutput

	err := h.txManager.WithTransaction(c.Request.Context(), func(txCtx context.Context) error {
		var err error
		output, err = h.workspaceService.RegisterWorkspace(txCtx, service.RegisterWorkspaceInput{
			OwnerEmail: req.OwnerEmail,
			OwnerName:  req.OwnerName,
			Plan:       req.Plan,
			TenantName: req.TenantName,
		})
		return err
	})

	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusAccepted, "Workspace registration accepted. Provisioning in progress.", RegisterWorkspaceResponse{
		TenantID: output.TenantID,
	})
}

func (h *WorkspaceHandler) GetServiceInfrastructure(c *gin.Context) {
	tenantID := c.Param("tenant_id")
	serviceName := c.Param("service_name")

	if tenantID == "" || serviceName == "" {
		httputil.WriteError(c, http.StatusBadRequest, "path must contain tenant_id and service_name")
		return
	}

	output, err := h.tenantInfrastructureService.GetServiceInfrastructure(c.Request.Context(), tenantID, serviceName)
	if err != nil {
		httputil.WriteError(c, http.StatusNotFound, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "", GetInfrastructureResponse{
		DBHost:     output.DBHost,
		DBPort:     output.DBPort,
		DBName:     output.DBName,
		DBUser:     output.DBUser,
		SchemaName: output.SchemaName,
	})
}
