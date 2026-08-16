package handler

import (
	"net/http"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"

	"github.com/gin-gonic/gin"
)

type InternalGetInfrastructureResponse struct {
	DBHost     string `json:"db_host"`
	DBPort     int    `json:"db_port"`
	DBName     string `json:"db_name"`
	DBUser     string `json:"db_user"`
	SchemaName string `json:"schema_name"`
}

type InternalTenantHandler struct {
	tenantInfraService InternalTenantInfrastructureService
}

func NewInternalTenantHandler(tenantInfraService InternalTenantInfrastructureService) *InternalTenantHandler {
	return &InternalTenantHandler{
		tenantInfraService: tenantInfraService,
	}
}

func (internalTenantHandler *InternalTenantHandler) GetServiceInfrastructure(c *gin.Context) {
	tenantID := c.Param("tenant_id")
	serviceName := c.Param("service_name")

	if tenantID == "" || serviceName == "" {
		httputil.WriteError(c, http.StatusBadRequest, "path must contain tenant_id and service_name")
		return
	}

	output, err := internalTenantHandler.tenantInfraService.GetServiceInfrastructure(c.Request.Context(), tenantID, serviceName)
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
