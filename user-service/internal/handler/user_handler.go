package handler

import (
	"context"
	"net/http"

	"user-service/internal/domain"
	"user-service/internal/httputil"
	"user-service/internal/infrastructure/authclient"
	"user-service/internal/service"

	"github.com/gin-gonic/gin"
)

type UserServiceInterface interface {
	ListUsers(ctx context.Context) ([]domain.User, error)
	UpdateUser(ctx context.Context, input service.UpdateUserServiceInput) error
	GetUserByID(ctx context.Context, userID string) (*domain.User, error)
}

type AuthClientInterface interface {
	AssignUserRole(ctx context.Context, authToken, userID, roleID string) (*authclient.UserRoleResponse, error)
	GetUserRole(ctx context.Context, authToken, userID string) (*authclient.UserRoleResponse, error)
	CreateRole(ctx context.Context, authToken string, input authclient.CreateRoleInput) (*authclient.Role, error)
	ListRoles(ctx context.Context, authToken string) ([]authclient.Role, error)
	ListPermissions(ctx context.Context, authToken string) ([]authclient.PermissionCatalogItem, error)
}

type UserHandler struct {
	userService UserServiceInterface
	authClient  AuthClientInterface
}

func NewUserHandler(userService UserServiceInterface, authClient AuthClientInterface) *UserHandler {
	return &UserHandler{
		userService: userService,
		authClient:  authClient,
	}
}

type UpdateUserRequest struct {
	Name string `json:"name" binding:"required"`
}

type AssignRoleRequest struct {
	RoleID string `json:"role_id" binding:"required"`
}

type CreateRoleRequest struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

func (userHandler *UserHandler) ListUsers(c *gin.Context) {
	users, err := userHandler.userService.ListUsers(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "user handler: failed to list users: "+err.Error())
		return
	}
	httputil.WriteSuccess(c, http.StatusOK, "Users retrieved successfully", users)
}

func (userHandler *UserHandler) UpdateMe(c *gin.Context) {
	userID := c.GetString("userID")
	if userID == "" {
		httputil.WriteError(c, http.StatusUnauthorized, "user handler: missing user_id in token claims")
		return
	}

	var req UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, http.StatusBadRequest, "user handler: invalid request body: "+err.Error())
		return
	}

	input := service.UpdateUserServiceInput{
		UserID: userID,
		Name:   req.Name,
	}

	if err := userHandler.userService.UpdateUser(c.Request.Context(), input); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "user handler: failed to update user profile: "+err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "User profile updated successfully", gin.H{
		"user_id": userID,
		"name":    req.Name,
	})
}

func (userHandler *UserHandler) GetUserRole(c *gin.Context) {
	targetUserID := c.Param("user_id")
	if targetUserID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "user handler: user_id route parameter is required")
		return
	}

	authToken := c.GetHeader("Authorization")
	res, err := userHandler.authClient.GetUserRole(c.Request.Context(), authToken, targetUserID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "user handler: failed to get user role: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "User role retrieved successfully", res)
}

func (userHandler *UserHandler) AssignUserRole(c *gin.Context) {
	targetUserID := c.Param("user_id")
	if targetUserID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "user handler: user_id route parameter is required")
		return
	}

	var req AssignRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, http.StatusBadRequest, "user handler: invalid request body: "+err.Error())
		return
	}

	authToken := c.GetHeader("Authorization")
	res, err := userHandler.authClient.AssignUserRole(c.Request.Context(), authToken, targetUserID, req.RoleID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "user handler: failed to assign user role: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "User role assigned successfully", res)
}

func (userHandler *UserHandler) CreateRole(c *gin.Context) {
	var req CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, http.StatusBadRequest, "user handler: invalid request body: "+err.Error())
		return
	}

	authToken := c.GetHeader("Authorization")
	input := authclient.CreateRoleInput{
		Name:        req.Name,
		Description: req.Description,
		Permissions: req.Permissions,
	}

	res, err := userHandler.authClient.CreateRole(c.Request.Context(), authToken, input)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "user handler: failed to create role: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusCreated, "Role created successfully", res)
}

func (userHandler *UserHandler) ListRoles(c *gin.Context) {
	authToken := c.GetHeader("Authorization")
	roles, err := userHandler.authClient.ListRoles(c.Request.Context(), authToken)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "user handler: failed to list roles: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Roles retrieved successfully", roles)
}

func (userHandler *UserHandler) ListPermissions(c *gin.Context) {
	authToken := c.GetHeader("Authorization")
	perms, err := userHandler.authClient.ListPermissions(c.Request.Context(), authToken)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "user handler: failed to list permissions: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Permissions retrieved successfully", perms)
}
