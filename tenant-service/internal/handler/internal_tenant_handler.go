package handler

import (
	"context"
	"net/http"

	"tenant-service/internal/domain"
	"tenant-service/internal/httputil"
	"tenant-service/internal/service"

	"github.com/gin-gonic/gin"
)

type InternalGetInfrastructureResponse struct {
	DBHost     string `json:"db_host"`
	DBPort     int    `json:"db_port"`
	DBName     string `json:"db_name"`
	DBUser     string `json:"db_user"`
	SchemaName string `json:"schema_name"`
}

type InternalGetTenantProfileResponse struct {
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Plan     string `json:"plan"`
	Status   string `json:"status"`
}

type InternalTenantInfrastructureService interface {
	GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*service.RoutingOutput, error)
}

// TenantProfileProvider resolves tenant control-plane metadata for internal consumers.
type TenantProfileProvider interface {
	GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error)
}

type InternalTenantHandler struct {
	tenantInfraService InternalTenantInfrastructureService
	profileProvider    TenantProfileProvider
}

func NewInternalTenantHandler(tenantInfraService InternalTenantInfrastructureService, profileProvider TenantProfileProvider) *InternalTenantHandler {
	return &InternalTenantHandler{
		tenantInfraService: tenantInfraService,
		profileProvider:    profileProvider,
	}
}

func (h *InternalTenantHandler) GetServiceInfrastructure(c *gin.Context) {
	tenantID := c.Param("tenant_id")
	serviceName := c.Param("service_name")

	if tenantID == "" || serviceName == "" {
		httputil.WriteError(c, http.StatusBadRequest, "path must contain tenant_id and service_name")
		return
	}

	output, err := h.tenantInfraService.GetServiceInfrastructure(c.Request.Context(), tenantID, serviceName)
	if err != nil {
		httputil.WriteError(c, http.StatusNotFound, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "", InternalGetInfrastructureResponse{
		DBHost:     output.DBHost,
		DBPort:     output.DBPort,
		DBName:     output.DBName,
		DBUser:     output.DBUser,
		SchemaName: output.SchemaName,
	})
}

func (h *InternalTenantHandler) GetTenantProfile(c *gin.Context) {
	tenantID := c.Param("tenant_id")
	if tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "path must contain tenant_id")
		return
	}

	tenant, err := h.profileProvider.GetTenantByID(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusNotFound, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "", InternalGetTenantProfileResponse{
		TenantID: tenant.ID,
		Name:     tenant.Name,
		Slug:     tenant.Slug,
		Plan:     tenant.Plan,
		Status:   tenant.Status,
	})
}
