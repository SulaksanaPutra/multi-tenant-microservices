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
func newRouter(
	authHandler *handler.AuthHandler,
	internalAuthHandler *handler.InternalAuthHandler,
	jwtManager *crypto.JWTManager,
	internalToken string,
) http.Handler {
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
		auth.POST("/credentials/setup", authHandler.SetupPassword)
		auth.POST("/login", authHandler.Login)
		auth.POST("/refresh", authHandler.Refresh)

		// Logout requires a valid JWT (to prevent anonymous token revocation abuse)
		auth.POST("/logout", middleware.RequireJWT(jwtManager), authHandler.Logout)
	}

	// Internal endpoints (authenticated via X-Internal-Service-Token)
	internalGroup := r.Group("/internal")
	internalGroup.Use(middleware.InternalAuthMiddleware(internalToken))
	{
		internalGroup.POST("/auth/setup-token", internalAuthHandler.CreateSetupToken)
	}

	return r
}
