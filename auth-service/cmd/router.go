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
	internalPermissionHandler *handler.InternalPermissionHandler,
	permissionHandler *handler.PermissionHandler,
	roleHandler *handler.RoleHandler,
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
	auth := r.Group("/api/auth")
	{
		auth.POST("/credentials/setup", authHandler.SetupPassword)
		auth.POST("/login", authHandler.Login)
		auth.POST("/refresh", authHandler.Refresh)

		// Logout requires a valid JWT (to prevent anonymous token revocation abuse)
		auth.POST("/logout", middleware.RequireJWT(jwtManager), authHandler.Logout)
	}

	// Tenant Admin API endpoints (JWT authenticated)
	api := r.Group("/api/auth")
	api.Use(middleware.RequireJWT(jwtManager))
	{
		// Permission catalog listing endpoint
		api.GET("/permissions", permissionHandler.ListPermissions)

		// Role management endpoints
		api.POST("/roles", roleHandler.CreateRole)
		api.GET("/roles", roleHandler.ListRoles)
		api.GET("/roles/:id", roleHandler.GetRole)
		api.PUT("/roles/:id/permissions", roleHandler.UpdateRolePermissions)
		api.DELETE("/roles/:id", roleHandler.DeleteRole)

		// User role assignment
		api.PUT("/users/:userID/role", roleHandler.AssignUserRole)
		api.GET("/users/:userID/role", roleHandler.GetUserRole)
	}

	// Internal endpoints (authenticated via X-Internal-Service-Token)
	internalGroup := r.Group("/internal/auth")
	internalGroup.Use(middleware.InternalAuthMiddleware(internalToken))
	{
		internalGroup.POST("/setup-token", internalAuthHandler.CreateSetupToken)
		internalGroup.POST("/permissions/register", internalPermissionHandler.RegisterPermissions)
		internalGroup.GET("/users/:userID/perm-version", internalPermissionHandler.GetUserPermissionVersion)
	}

	return r
}
