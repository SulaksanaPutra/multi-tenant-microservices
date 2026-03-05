package handler

import (
	"context"
	"net/http"

	"auth-service/internal/domain"
	"auth-service/internal/httputil"

	"github.com/gin-gonic/gin"
)

type PermissionAppService interface {
	ListPermissions(ctx context.Context) ([]domain.Permission, error)
}

type PermissionHandler struct {
	permService PermissionAppService
}

func NewPermissionHandler(permService PermissionAppService) *PermissionHandler {
	return &PermissionHandler{permService: permService}
}

func (h *PermissionHandler) ListPermissions(c *gin.Context) {
	permissions, err := h.permService.ListPermissions(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Permissions retrieved successfully", permissions)
}
