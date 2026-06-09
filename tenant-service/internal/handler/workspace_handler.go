package handler

import (
	"context"
	"net/http"

	"tenant-service/internal/httputil"
	"tenant-service/internal/service"

	"github.com/gin-gonic/gin"
)

type RegisterWorkspaceRequest struct {
	OwnerEmail string `json:"owner_email" binding:"required,email,max=255"`
	OwnerName  string `json:"owner_name"  binding:"required,max=255"`
	Plan       string `json:"plan"        binding:"required,oneof=shared dedicated"`
	TenantName string `json:"tenant_name" binding:"required,max=255"`
}

type RegisterWorkspaceResponse struct {
	Status string `json:"status"`
}

// TxManager is the consumer-side interface expected by WorkspaceHandler.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

// WorkspaceService is the consumer-side interface expected by WorkspaceHandler.
type WorkspaceService interface {
	RegisterWorkspace(ctx context.Context, input service.RegisterWorkspaceInput) (*service.RegisterWorkspaceOutput, error)
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
		Status: output.Status,
	})
}
