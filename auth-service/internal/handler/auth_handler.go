package handler

import (
	"context"
	"errors"
	"net/http"

	"auth-service/internal/crypto"
	"auth-service/internal/domain"
	"auth-service/internal/httputil"
	"auth-service/internal/middleware"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

// AuthServiceIface is the consumer-side interface expected by AuthHandler.
type AuthServiceIface interface {
	SetCredentials(ctx context.Context, input service.SetCredentialsInput) error
	Login(ctx context.Context, input service.LoginInput) (*service.TokenPair, error)
	RefreshToken(ctx context.Context, rawRefreshToken string) (*service.TokenPair, error)
	Logout(ctx context.Context, rawRefreshToken string) error
}

// AuthHandler handles HTTP requests for authentication endpoints.
type AuthHandler struct {
	authService AuthServiceIface
	jwtManager  *crypto.JWTManager
}

// NewAuthHandler constructs an AuthHandler with required dependencies.
func NewAuthHandler(authService AuthServiceIface, jwtManager *crypto.JWTManager) *AuthHandler {
	return &AuthHandler{
		authService: authService,
		jwtManager:  jwtManager,
	}
}

// --- Request / Response types ---

type setCredentialsRequest struct {
	UserID   string `json:"user_id"   binding:"required"`
	TenantID string `json:"tenant_id" binding:"required"`
	Email    string `json:"email"     binding:"required,email"`
	Password string `json:"password"  binding:"required,min=8"`
}

type loginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// --- Handlers ---

// SetCredentials establishes or replaces a user's password credential.
//
// POST /auth/credentials/set
//
// TEMPORARY — NON-PRODUCTION SCAFFOLDING (Stage 1 only)
// This endpoint has no identity verification beyond the provided fields.
// It MUST be replaced by an email-invite / token-gated flow before production
// or before the OAuth 2.0 stage implementation.
func (h *AuthHandler) SetCredentials(c *gin.Context) {
	var req setCredentialsRequest
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
		httputil.WriteError(c, http.StatusInternalServerError, "failed to set credentials: "+err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "Credentials set successfully", nil)
}

// Login authenticates a user and issues a JWT access token + opaque refresh token.
//
// POST /auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
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
		httputil.WriteError(c, http.StatusInternalServerError, "login failed: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Login successful", tokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
	})
}

// Refresh validates an opaque refresh token and issues a new JWT + rotated refresh token.
//
// POST /auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	pair, err := h.authService.RefreshToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, domain.ErrTokenNotFound) ||
			errors.Is(err, domain.ErrTokenRevoked) ||
			errors.Is(err, domain.ErrTokenExpired) {
			httputil.WriteError(c, http.StatusUnauthorized, "refresh token is invalid, expired, or revoked")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, "token refresh failed: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Token refreshed", tokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
	})
}

// Logout revokes the caller's refresh token. Requires a valid JWT Bearer token.
//
// POST /auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	var req logoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	if err := h.authService.Logout(c.Request.Context(), req.RefreshToken); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "logout failed: "+err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "Logged out successfully", nil)
}

// JWKS serves the RSA public key set for JWT verification by downstream services.
//
// GET /.well-known/jwks.json
func (h *AuthHandler) JWKS(c *gin.Context) {
	jwksBytes, err := h.jwtManager.BuildJWKS()
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "failed to build JWKS")
		return
	}

	_ = middleware.ContextKeyUserID // ensure import is used
	c.Data(http.StatusOK, "application/json", jwksBytes)
}
