package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
	"tenant-service/internal/service"

	"github.com/gin-gonic/gin"
)

type TenantHandler struct {
	txManager        TxManager
	workspaceService TenantServiceInterface
}

func NewTenantHandler(txManager TxManager, workspaceService TenantServiceInterface) *TenantHandler {
	return &TenantHandler{
		txManager:        txManager,
		workspaceService: workspaceService,
	}
}

type UpdateTenantRequest struct {
	Name       string  `json:"name" binding:"required"`
	Slug       string  `json:"slug"`
	OwnerEmail *string `json:"owner_email"`
	OwnerName  *string `json:"owner_name"`
}

type ChangePlanRequest struct {
	Plan string `json:"plan" binding:"required"`
}

// TenantResponse is the transport DTO for a tenant profile returned to API consumers.
type TenantResponse struct {
	TenantID   string    `json:"tenant_id"`
	Name       string    `json:"name"`
	Slug       string    `json:"slug"`
	OwnerEmail string    `json:"owner_email"`
	OwnerName  string    `json:"owner_name"`
	Plan       string    `json:"plan"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ChangeTenantPlanResponse is the transport DTO for a tenant isolation plan mutation.
type ChangeTenantPlanResponse struct {
	TenantID string `json:"tenant_id"`
	Plan     string `json:"plan"`
}

func toTenantResponse(tenant service.TenantOutput) TenantResponse {
	return TenantResponse{
		TenantID:   tenant.ID,
		Name:       tenant.Name,
		Slug:       tenant.Slug,
		OwnerEmail: tenant.OwnerEmail,
		OwnerName:  tenant.OwnerName,
		Plan:       tenant.Plan,
		Status:     tenant.Status,
		CreatedAt:  tenant.CreatedAt,
		UpdatedAt:  tenant.UpdatedAt,
	}
}

func (tenantHandler *TenantHandler) GetTenantMe(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		httputil.WriteError(c, http.StatusUnauthorized, "tenant handler: missing tenant_id in token claims")
		return
	}

	tenant, err := tenantHandler.workspaceService.GetTenantByID(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "tenant handler: failed to retrieve tenant: "+err.Error())
		return
	}
	if tenant == nil {
		httputil.WriteError(c, http.StatusNotFound, "tenant handler: tenant not found")
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Tenant profile retrieved successfully", toTenantResponse(*tenant))
}

func (tenantHandler *TenantHandler) UpdateTenantMe(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		httputil.WriteError(c, http.StatusUnauthorized, "tenant handler: missing tenant_id in token claims")
		return
	}

	var req UpdateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, http.StatusBadRequest, "tenant handler: invalid request body: "+err.Error())
		return
	}

	input := service.UpdateTenantServiceInput{
		TenantID:   tenantID,
		Name:       req.Name,
		Slug:       req.Slug,
		OwnerEmail: req.OwnerEmail,
		OwnerName:  req.OwnerName,
	}

	if err := tenantHandler.workspaceService.UpdateTenant(c.Request.Context(), input); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "tenant handler: failed to update tenant profile: "+err.Error())
		return
	}

	// Return the persisted tenant as the response so clients can reset their
	// local tenant state from the authoritative updated record.
	tenant, err := tenantHandler.workspaceService.GetTenantByID(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "tenant handler: failed to reload tenant after update: "+err.Error())
		return
	}
	if tenant == nil {
		httputil.WriteError(c, http.StatusNotFound, "tenant handler: tenant not found after update")
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Tenant profile updated successfully", toTenantResponse(*tenant))
}

func (tenantHandler *TenantHandler) ChangeTenantPlanMe(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		httputil.WriteError(c, http.StatusUnauthorized, "tenant handler: missing tenant_id in token claims")
		return
	}

	var req ChangePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, http.StatusBadRequest, "tenant handler: invalid request body: "+err.Error())
		return
	}

	input := service.ChangeTenantPlanInput{
		TenantID: tenantID,
		Plan:     req.Plan,
	}

	execChangePlan := func(ctx context.Context) error {
		return tenantHandler.workspaceService.ChangeTenantPlan(ctx, input)
	}

	var err error
	if tenantHandler.txManager != nil {
		err = tenantHandler.txManager.WithTransaction(c.Request.Context(), execChangePlan)
	} else {
		err = execChangePlan(c.Request.Context())
	}

	if err != nil {
		httputil.WriteError(c, http.StatusBadRequest, "tenant handler: failed to change tenant plan: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Tenant plan updated successfully", ChangeTenantPlanResponse{
		TenantID: tenantID,
		Plan:     req.Plan,
	})
}
