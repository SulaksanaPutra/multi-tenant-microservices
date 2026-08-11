package handler

import (
	"context"
	"errors"
	"net/http"

	"auth-service/internal/crypto"
	"auth-service/internal/domain"
	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type AuthService interface {
	SetupPassword(ctx context.Context, input service.SetupPasswordInput) (*service.TokenPair, error)
	Login(ctx context.Context, input service.LoginInput) (*service.LoginOutput, error)
	SelectWorkspace(ctx context.Context, input service.SelectWorkspaceInput) (*service.TokenPair, error)
	RefreshToken(ctx context.Context, input service.RefreshTokenInput) (*service.TokenPair, error)
	Logout(ctx context.Context, input service.LogoutInput) error
}

type SetupPasswordRequest struct {
	Token    string `json:"token"    binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
}

type LoginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type SelectTenantRequest struct {
	ExchangeToken string `json:"exchange_token" binding:"required"`
	TenantID      string `json:"tenant_id"      binding:"required"`
}

type WorkspaceInfo struct {
	TenantID string `json:"tenant_id"`
}

type LoginResponse struct {
	RequiresWorkspace bool            `json:"requires_workspace,omitempty"`
	ExchangeToken     string          `json:"exchange_token,omitempty"`
	Workspaces        []WorkspaceInfo `json:"workspaces,omitempty"`
}

type TokenPairResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type RefreshTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type AuthHandler struct {
	authService AuthService
	jwtManager  *crypto.JWTManager
}

func NewAuthHandler(authService AuthService, jwtManager *crypto.JWTManager) *AuthHandler {
	return &AuthHandler{
		authService: authService,
		jwtManager:  jwtManager,
	}
}

func (h *AuthHandler) SetupPassword(c *gin.Context) {
	var req SetupPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	pair, err := h.authService.SetupPassword(c.Request.Context(), service.SetupPasswordInput{
		Token:    req.Token,
		Password: req.Password,
	})
	if err != nil {
		if errors.Is(err, domain.ErrTokenNotFound) ||
			errors.Is(err, domain.ErrTokenExpired) ||
			errors.Is(err, domain.ErrTokenAlreadyUsed) {
			httputil.WriteError(c, http.StatusBadRequest, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Password setup successful", TokenPairResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
	})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	res, err := h.authService.Login(c.Request.Context(), service.LoginInput{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		if errors.Is(err, domain.ErrInvalidCredentials) {
			httputil.WriteError(c, http.StatusUnauthorized, "invalid email or password")
			return
		}
		if errors.Is(err, domain.ErrNoTenantMembership) {
			httputil.WriteError(c, http.StatusUnauthorized, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	workspaceResponses := make([]WorkspaceInfo, 0, len(res.Workspaces))
	for _, ws := range res.Workspaces {
		workspaceResponses = append(workspaceResponses, WorkspaceInfo{
			TenantID: ws.TenantID,
		})
	}
	httputil.WriteSuccess(c, http.StatusOK, "Multiple workspace accounts found. Please select a workspace.", LoginResponse{
		RequiresWorkspace: true,
		ExchangeToken:     res.ExchangeToken,
		Workspaces:        workspaceResponses,
	})
}

func (h *AuthHandler) SelectTenant(c *gin.Context) {
	var req SelectTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	pair, err := h.authService.SelectWorkspace(c.Request.Context(), service.SelectWorkspaceInput{
		ExchangeToken: req.ExchangeToken,
		TenantID:      req.TenantID,
	})
	if err != nil {
		if errors.Is(err, domain.ErrTokenNotFound) ||
			errors.Is(err, domain.ErrTokenExpired) ||
			errors.Is(err, domain.ErrTokenAlreadyUsed) ||
			errors.Is(err, domain.ErrTenantIDRequired) ||
			errors.Is(err, domain.ErrTenantMembershipNotFound) {
			httputil.WriteError(c, http.StatusBadRequest, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Workspace selection successful", TokenPairResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
	})
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req RefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	pair, err := h.authService.RefreshToken(c.Request.Context(), service.RefreshTokenInput{
		RefreshToken: req.RefreshToken,
	})
	if err != nil {
		if errors.Is(err, domain.ErrTokenNotFound) ||
			errors.Is(err, domain.ErrTokenRevoked) ||
			errors.Is(err, domain.ErrTokenExpired) {
			httputil.WriteError(c, http.StatusUnauthorized, "refresh token is invalid, expired, or revoked")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Token refreshed", RefreshTokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
	})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	if err := h.authService.Logout(c.Request.Context(), service.LogoutInput{
		RefreshToken: req.RefreshToken,
	}); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "Logged out successfully", nil)
}

func (h *AuthHandler) JWKS(c *gin.Context) {
	jwksBytes, err := h.jwtManager.BuildJWKS()
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "failed to build JWKS")
		return
	}

	c.Data(http.StatusOK, "application/json", jwksBytes)
}
