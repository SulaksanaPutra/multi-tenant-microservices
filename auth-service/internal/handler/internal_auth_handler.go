package handler

import (
	"context"
	"net/http"

	"auth-service/internal/httputil"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type InternalAuthService interface {
	CreatePasswordSetupToken(ctx context.Context, input service.CreateSetupTokenInput) (string, error)
}

type CreateSetupTokenRequest struct {
	UserID   string `json:"user_id"   binding:"required"`
	TenantID string `json:"tenant_id" binding:"required"`
	Email    string `json:"email"     binding:"required,email"`
}

type CreateSetupTokenResponse struct {
	Token string `json:"token"`
}

type InternalAuthHandler struct {
	authService InternalAuthService
}

func NewInternalAuthHandler(authService InternalAuthService) *InternalAuthHandler {
	return &InternalAuthHandler{authService: authService}
}

func (h *InternalAuthHandler) CreateSetupToken(c *gin.Context) {
	var req CreateSetupTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	token, err := h.authService.CreatePasswordSetupToken(c.Request.Context(), service.CreateSetupTokenInput{
		UserID:   req.UserID,
		TenantID: req.TenantID,
		Email:    req.Email,
	})
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Setup token generated successfully", CreateSetupTokenResponse{
		Token: token,
	})
}
