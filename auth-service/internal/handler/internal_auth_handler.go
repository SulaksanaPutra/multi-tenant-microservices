package handler

import (
	"net/http"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)


type InternalCreateSetupTokenRequest struct {
	UserID   string `json:"user_id"   binding:"required"`
	TenantID string `json:"tenant_id" binding:"required"`
	Email    string `json:"email"     binding:"required,email"`
}

type InternalCreateSetupTokenResponse struct {
	Token string `json:"token"`
}

type InternalAuthHandler struct {
	internalAuthAppService InternalAuthAppService
}

func NewInternalAuthHandler(internalAuthAppService InternalAuthAppService) *InternalAuthHandler {
	return &InternalAuthHandler{internalAuthAppService: internalAuthAppService}
}

func (h *InternalAuthHandler) CreateSetupToken(c *gin.Context) {
	var req InternalCreateSetupTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	token, err := h.internalAuthAppService.CreatePasswordSetupToken(c.Request.Context(), service.InternalCreateSetupTokenInput{
		UserID:   req.UserID,
		TenantID: req.TenantID,
		Email:    req.Email,
	})
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Setup token generated successfully", InternalCreateSetupTokenResponse{
		Token: token,
	})
}
