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
		auth.POST("/select-tenant", authHandler.SelectTenant)
		auth.POST("/refresh", authHandler.Refresh)

		// Logout requires a valid JWT (to prevent anonymous token revocation abuse)
		auth.POST("/logout", middleware.RequireJWT(jwtManager), authHandler.Logout)
	}

	// Tenant Admin API endpoints (JWT authenticated + RBAC permission gated)
	api := r.Group("/api/auth")
	api.Use(middleware.RequireJWT(jwtManager))
	{
		// Read-only RBAC endpoints (auth:roles:read)
		api.GET("/permissions", middleware.RequirePermission("auth:roles:read"), permissionHandler.ListPermissions)
		api.GET("/roles", middleware.RequirePermission("auth:roles:read"), roleHandler.ListRoles)
		api.GET("/roles/:id", middleware.RequirePermission("auth:roles:read"), roleHandler.GetRole)

		// Static bulk assignment lookup — registered before the parameterized
		// :userID route; Gin radix tree gives static segments precedence.
		api.GET("/users/roles", middleware.RequirePermission("auth:roles:read"), roleHandler.ListUserRoles)
		api.GET("/users/:user_id/role", middleware.RequirePermission("auth:roles:read"), roleHandler.GetUserRole)

		// Write endpoints (auth:roles:manage)
		api.POST("/roles", middleware.RequirePermission("auth:roles:manage"), roleHandler.CreateRole)
		api.PUT("/roles/:id/permissions", middleware.RequirePermission("auth:roles:manage"), roleHandler.UpdateRolePermissions)
		api.DELETE("/roles/:id", middleware.RequirePermission("auth:roles:manage"), roleHandler.DeleteRole)
		api.PUT("/users/:user_id/role", middleware.RequirePermission("auth:roles:manage"), roleHandler.AssignUserRole)
	}

	// Internal endpoints (authenticated via X-Internal-Service-Token)
	internalGroup := r.Group("/internal/auth")
	internalGroup.Use(middleware.InternalAuthMiddleware(internalToken))
	{
		internalGroup.POST("/setup-token", internalAuthHandler.CreateSetupToken)
		internalGroup.POST("/permissions/register", internalPermissionHandler.RegisterPermissions)
		internalGroup.GET("/users/:user_id/perm-version", internalPermissionHandler.GetUserPermissionVersion)
	}

	return r
}
