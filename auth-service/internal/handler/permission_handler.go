package handler

import (
	"net/http"
	"time"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)


// PermissionResponse is the transport DTO for a permission catalog entry.
type PermissionResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Service     string    `json:"service"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PermissionHandler struct {
	permissionService PermissionService
}

func NewPermissionHandler(permissionService PermissionService) *PermissionHandler {
	return &PermissionHandler{permissionService: permissionService}
}

func toPermissionResponse(permission service.PermissionOutput) PermissionResponse {
	return PermissionResponse{
		ID:          permission.ID,
		Name:        permission.Name,
		Service:     permission.Service,
		Description: permission.Description,
		CreatedAt:   permission.CreatedAt,
		UpdatedAt:   permission.UpdatedAt,
	}
}

func (h *PermissionHandler) ListPermissions(c *gin.Context) {
	permissions, err := h.permissionService.ListPermissions(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	permissionResponses := make([]PermissionResponse, len(permissions))
	for i, permission := range permissions {
		permissionResponses[i] = toPermissionResponse(permission)
	}

	httputil.WriteSuccess(c, http.StatusOK, "Permissions retrieved successfully", permissionResponses)
}
