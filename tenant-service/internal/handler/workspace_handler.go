package handler

import (
	"context"
	"net/http"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
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
	Status string `json:"status"`
}

type WorkspaceHandler struct {
	txManager        TxManager
	workspaceService WorkspaceService
}

func NewWorkspaceHandler(txManager TxManager, workspaceService WorkspaceService) *WorkspaceHandler {
	return &WorkspaceHandler{
		txManager:        txManager,
		workspaceService: workspaceService,
	}
}

func (workspaceHandler *WorkspaceHandler) RegisterWorkspace(c *gin.Context) {
	var req RegisterWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	var output *service.RegisterWorkspaceOutput

	err := workspaceHandler.txManager.WithTransaction(c.Request.Context(), func(txCtx context.Context) error {
		var err error
		output, err = workspaceHandler.workspaceService.RegisterWorkspace(txCtx, service.RegisterWorkspaceInput{
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
		Status: output.Status,
	})
}
