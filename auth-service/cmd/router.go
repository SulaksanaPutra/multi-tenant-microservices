package main

import (
	"net/http"

	"auth-service/internal/crypto"
	"auth-service/internal/handler"
	"auth-service/internal/httputil"
	"auth-service/internal/middleware"

	"github.com/gin-gonic/gin"
)

// newRouter initializes all HTTP routes for Auth Service.
func newRouter(authHandler *handler.AuthHandler, jwtManager *crypto.JWTManager) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	// Health check (unauthenticated)
	r.GET("/health", func(c *gin.Context) {
		httputil.WriteSuccess[any](c, http.StatusOK, "OK", nil)
	})

	// JWKS endpoint — public key for downstream service JWT verification
	r.GET("/.well-known/jwks.json", authHandler.JWKS)

	// Auth endpoints (unauthenticated)
	auth := r.Group("/auth")
	{
		// TEMPORARY — NON-PRODUCTION SCAFFOLDING (Stage 1 only)
		// Replace with email-invite/reset flow before OAuth 2.0 stage.
		auth.POST("/credentials/set", authHandler.SetCredentials)

		auth.POST("/login", authHandler.Login)
		auth.POST("/refresh", authHandler.Refresh)

		// Logout requires a valid JWT (to prevent anonymous token revocation abuse)
		auth.POST("/logout", middleware.RequireJWT(jwtManager), authHandler.Logout)
	}

	return r
}
