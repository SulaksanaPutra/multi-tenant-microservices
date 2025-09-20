package handler

import (
	"context"
	"net/http"

	"tenant-service/internal/service"
	"tenant-service/internal/txctx"
	"tenant-service/internal/utils"

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

type UpdateInfrastructureRequest struct {
	ServiceName string `json:"service_name" binding:"required"`
	DSN         string `json:"dsn"          binding:"required"`
	SchemaName  string `json:"schema_name"`
}

type ServiceInfrastructureResponse struct {
	DSN        string `json:"dsn"`
	SchemaName string `json:"schema_name"`
}

type WorkspaceHandler struct {
	txManager        txctx.TxManager
	workspaceService service.WorkspaceService
}

func NewWorkspaceHandler(txManager txctx.TxManager, workspaceSvc service.WorkspaceService) *WorkspaceHandler {
	return &WorkspaceHandler{
		txManager:        txManager,
		workspaceService: workspaceSvc,
	}
}

func (h *WorkspaceHandler) RegisterWorkspace(c *gin.Context) {
	var req RegisterWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.WriteValidationError(c, err)
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
		utils.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteSuccess(c, http.StatusAccepted, "Workspace registration accepted. Provisioning in progress.", RegisterWorkspaceResponse{
		TenantID: output.TenantID,
	})
}

func (h *WorkspaceHandler) UpdateInfrastructure(c *gin.Context) {
	tenantID := c.Param("tenant_id")
	if tenantID == "" {
		utils.WriteError(c, http.StatusBadRequest, "missing tenant_id in path")
		return
	}

	var req UpdateInfrastructureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.WriteValidationError(c, err)
		return
	}

	err := h.txManager.WithTransaction(c.Request.Context(), func(txCtx context.Context) error {
		return h.workspaceService.HandleInfrastructureUpdate(txCtx, service.InfraUpdateInput{
			TenantID:    tenantID,
			ServiceName: req.ServiceName,
			DSN:         req.DSN,
			SchemaName:  req.SchemaName,
		})
	})

	if err != nil {
		utils.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteSuccess[any](c, http.StatusOK, "Infrastructure registered", nil)
}

func (h *WorkspaceHandler) GetServiceInfrastructure(c *gin.Context) {
	tenantID := c.Param("tenant_id")
	serviceName := c.Param("service_name")

	if tenantID == "" || serviceName == "" {
		utils.WriteError(c, http.StatusBadRequest, "path must contain tenant_id and service_name")
		return
	}

	output, err := h.workspaceService.GetServiceDSN(c.Request.Context(), tenantID, serviceName)
	if err != nil {
		utils.WriteError(c, http.StatusNotFound, err.Error())
		return
	}

	utils.WriteSuccess(c, http.StatusOK, "", ServiceInfrastructureResponse{
		DSN:        output.DSN,
		SchemaName: output.SchemaName,
	})
}
