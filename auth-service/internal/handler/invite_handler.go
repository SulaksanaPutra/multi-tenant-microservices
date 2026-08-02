package handler

import (
	"context"
	"errors"
	"net/http"

	"auth-service/internal/domain"
	"auth-service/internal/httputil"
	"auth-service/internal/middleware"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type InvitationService interface {
	InviteUser(ctx context.Context, input service.InviteUserInput) (*service.InviteUserOutput, error)
}

type InviteUserRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	RoleID   string `json:"role_id"  binding:"required"`
	TenantID string `json:"tenant_id"`
}

type InviteUserResponse struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	RoleID string `json:"role_id"`
	Token  string `json:"token"`
}

type InviteHandler struct {
	invitationService InvitationService
}

func NewInviteHandler(invitationService InvitationService) *InviteHandler {
	return &InviteHandler{invitationService: invitationService}
}

func (h *InviteHandler) Invite(c *gin.Context) {
	var req InviteUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		tenantID = req.TenantID
	}
	if tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "tenant_id parameter or JWT token claim is required")
		return
	}

	invitedBy := c.GetString(middleware.ContextKeyUserID)

	invitee, err := h.invitationService.InviteUser(c.Request.Context(), service.InviteUserInput{
		Email:     req.Email,
		RoleID:    req.RoleID,
		TenantID:  tenantID,
		InvitedBy: invitedBy,
	})
	if err != nil {
		if errors.Is(err, domain.ErrRoleNotFound) {
			httputil.WriteError(c, http.StatusNotFound, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusCreated, "User invited successfully", InviteUserResponse{
		UserID: invitee.UserID,
		Email:  invitee.Email,
		RoleID: invitee.RoleID,
		Token:  invitee.Token,
	})
}
