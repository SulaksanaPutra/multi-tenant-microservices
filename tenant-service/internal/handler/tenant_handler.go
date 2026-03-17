package handler

import (
	"context"
	"net/http"

	"tenant-service/internal/domain"
	"tenant-service/internal/httputil"
	"tenant-service/internal/service"

	"github.com/gin-gonic/gin"
)

type TenantServiceInterface interface {
	GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error)
	ListTenants(ctx context.Context, tenantID string) ([]domain.Tenant, error)
	UpdateTenant(ctx context.Context, input service.UpdateTenantServiceInput) error
	ChangeTenantPlan(ctx context.Context, input service.ChangeTenantPlanInput) error
}

type TenantHandler struct {
	workspaceService TenantServiceInterface
}

func NewTenantHandler(workspaceService TenantServiceInterface) *TenantHandler {
	return &TenantHandler{
		workspaceService: workspaceService,
	}
}

type UpdateTenantRequest struct {
	Name       string `json:"name" binding:"required"`
	Slug       string `json:"slug"`
	OwnerEmail string `json:"owner_email" binding:"required"`
	OwnerName  string `json:"owner_name"`
}

type ChangePlanRequest struct {
	Plan string `json:"plan" binding:"required"`
}

func (tenantHandler *TenantHandler) GetTenantMe(c *gin.Context) {
	tenantID := c.GetString("tenantID")
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

	httputil.WriteSuccess(c, http.StatusOK, "Tenant profile retrieved successfully", tenant)
}

func (tenantHandler *TenantHandler) UpdateTenantMe(c *gin.Context) {
	tenantID := c.GetString("tenantID")
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

	httputil.WriteSuccess[any](c, http.StatusOK, "Tenant profile updated successfully", gin.H{
		"tenant_id":   tenantID,
		"name":        req.Name,
		"owner_email": req.OwnerEmail,
	})
}

func (tenantHandler *TenantHandler) ChangeTenantPlanMe(c *gin.Context) {
	tenantID := c.GetString("tenantID")
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

	if err := tenantHandler.workspaceService.ChangeTenantPlan(c.Request.Context(), input); err != nil {
		httputil.WriteError(c, http.StatusBadRequest, "tenant handler: failed to change tenant plan: "+err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "Tenant plan updated successfully", gin.H{
		"tenant_id": tenantID,
		"plan":      req.Plan,
	})
}
