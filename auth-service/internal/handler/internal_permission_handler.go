package handler

import (
	"context"
	"errors"
	"net/http"

	"auth-service/internal/domain"
	"auth-service/internal/httputil"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type InternalPermissionService interface {
	RegisterPermissions(ctx context.Context, input service.InternalRegisterPermissionsInput) error
	GetUserPermissionVersion(ctx context.Context, userID, tenantID string) (int64, error)
}

type InternalPermissionItemRequest struct {
	Name        string `json:"name"        binding:"required"`
	Description string `json:"description"`
}

type InternalRegisterPermissionsRequest struct {
	Service     string                          `json:"service"     binding:"required"`
	Permissions []InternalPermissionItemRequest `json:"permissions" binding:"required,gt=0"`
}

type InternalPermissionVersionResponse struct {
	UserID      string `json:"user_id"`
	TenantID    string `json:"tenant_id"`
	PermVersion int64  `json:"perm_version"`
}

type InternalPermissionHandler struct {
	permissionService InternalPermissionService
}

func NewInternalPermissionHandler(permissionService InternalPermissionService) *InternalPermissionHandler {
	return &InternalPermissionHandler{permissionService: permissionService}
}

func (h *InternalPermissionHandler) RegisterPermissions(c *gin.Context) {
	var req InternalRegisterPermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	items := make([]service.InternalRegisterPermissionItem, len(req.Permissions))
	for i, item := range req.Permissions {
		items[i] = service.InternalRegisterPermissionItem{
			Name:        item.Name,
			Description: item.Description,
		}
	}

	if err := h.permissionService.RegisterPermissions(c.Request.Context(), service.InternalRegisterPermissionsInput{
		Service:     req.Service,
		Permissions: items,
	}); err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			httputil.WriteError(c, http.StatusBadRequest, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "Permissions registered successfully", nil)
}

func (h *InternalPermissionHandler) GetUserPermissionVersion(c *gin.Context) {
	userID := c.Param("userID")
	tenantID := c.Query("tenant_id")

	if userID == "" || tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "user_id and tenant_id query parameters are required")
		return
	}

	ver, err := h.permissionService.GetUserPermissionVersion(c.Request.Context(), userID, tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Permission version retrieved successfully", InternalPermissionVersionResponse{
		UserID:      userID,
		TenantID:    tenantID,
		PermVersion: ver,
	})
}
