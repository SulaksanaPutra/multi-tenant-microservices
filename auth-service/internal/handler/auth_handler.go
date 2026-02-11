package handler

import (
	"context"
	"errors"
	"net/http"

	"auth-service/internal/crypto"
	"auth-service/internal/domain"
	"auth-service/internal/httputil"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type AuthService interface {
	SetCredentials(ctx context.Context, input service.SetCredentialsInput) error
	Login(ctx context.Context, input service.LoginInput) (*service.TokenPair, error)
	RefreshToken(ctx context.Context, input service.RefreshTokenInput) (*service.TokenPair, error)
	Logout(ctx context.Context, input service.LogoutInput) error
}

type SetCredentialsRequest struct {
	UserID   string `json:"user_id"   binding:"required"`
	TenantID string `json:"tenant_id" binding:"required"`
	Email    string `json:"email"     binding:"required,email"`
	Password string `json:"password"  binding:"required,min=8"`
}

type LoginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
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

func (h *AuthHandler) SetCredentials(c *gin.Context) {
	var req SetCredentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	if err := h.authService.SetCredentials(c.Request.Context(), service.SetCredentialsInput{
		UserID:   req.UserID,
		TenantID: req.TenantID,
		Email:    req.Email,
		Password: req.Password,
	}); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "Credentials set successfully", nil)
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	pair, err := h.authService.Login(c.Request.Context(), service.LoginInput{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		if errors.Is(err, domain.ErrInvalidCredentials) {
			httputil.WriteError(c, http.StatusUnauthorized, "invalid email or password")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Login successful", LoginResponse{
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
