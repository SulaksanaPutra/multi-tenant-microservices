package handler

import (
	"context"
	"net/http"

	"auth-service/internal/domain"
	"auth-service/internal/httputil"
	"auth-service/internal/repository"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type PermissionAppService interface {
	RegisterPermissions(ctx context.Context, input service.RegisterPermissionsInput) error
	ListPermissions(ctx context.Context) ([]domain.Permission, error)
	GetUserPermissionVersion(ctx context.Context, userID, tenantID string) (int64, error)
}

type PermissionItemRequest struct {
	Name        string `json:"name"        binding:"required"`
	Description string `json:"description"`
}

type RegisterPermissionsRequest struct {
	Service     string                  `json:"service"     binding:"required"`
	Permissions []PermissionItemRequest `json:"permissions" binding:"required,gt=0"`
}

type PermissionVersionResponse struct {
	UserID      string `json:"user_id"`
	TenantID    string `json:"tenant_id"`
	PermVersion int64  `json:"perm_version"`
}

type PermissionHandler struct {
	permService PermissionAppService
}

func NewPermissionHandler(permService PermissionAppService) *PermissionHandler {
	return &PermissionHandler{permService: permService}
}

func (h *PermissionHandler) RegisterPermissions(c *gin.Context) {
	var req RegisterPermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	items := make([]repository.RegisterPermissionItem, len(req.Permissions))
	for i, item := range req.Permissions {
		items[i] = repository.RegisterPermissionItem{
			Name:        item.Name,
			Description: item.Description,
		}
	}

	if err := h.permService.RegisterPermissions(c.Request.Context(), service.RegisterPermissionsInput{
		Service:     req.Service,
		Permissions: items,
	}); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "Permissions registered successfully", nil)
}

func (h *PermissionHandler) ListPermissions(c *gin.Context) {
	permissions, err := h.permService.ListPermissions(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Permissions retrieved successfully", permissions)
}

func (h *PermissionHandler) GetUserPermissionVersion(c *gin.Context) {
	userID := c.Param("userID")
	tenantID := c.Query("tenant_id")

	if userID == "" || tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "user_id and tenant_id query parameters are required")
		return
	}

	ver, err := h.permService.GetUserPermissionVersion(c.Request.Context(), userID, tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Permission version retrieved successfully", PermissionVersionResponse{
		UserID:      userID,
		TenantID:    tenantID,
		PermVersion: ver,
	})
}
