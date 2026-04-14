package handler

import (
	"context"
	"net/http"

	"user-service/internal/domain"
	"user-service/internal/httputil"
	"user-service/internal/middleware"
	"user-service/internal/service"

	"github.com/gin-gonic/gin"
)

type UserServiceInterface interface {
	ListUsers(ctx context.Context, tenantID string) ([]domain.User, error)
	UpdateUser(ctx context.Context, input service.UpdateUserServiceInput) error
}

type UserHandler struct {
	userService UserServiceInterface
}

func NewUserHandler(userService UserServiceInterface) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

type ListUsersResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type UpdateUserRequest struct {
	Name string `json:"name" binding:"required"`
}

func (userHandler *UserHandler) ListUsers(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		httputil.WriteError(c, http.StatusUnauthorized, "user handler: missing tenant_id in token claims")
		return
	}

	users, err := userHandler.userService.ListUsers(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "user handler: failed to list users: "+err.Error())
		return
	}

	userResponses := make([]ListUsersResponse, 0, len(users))
	for _, u := range users {
		userResponses = append(userResponses, ListUsersResponse{
			ID:    u.ID,
			Email: u.Email,
			Name:  u.Name,
		})
	}
	httputil.WriteSuccess(c, http.StatusOK, "Users retrieved successfully", userResponses)
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
