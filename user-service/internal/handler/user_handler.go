package handler

import (
	"context"
	"errors"
	"net/http"

	"user-service/internal/domain"
	"user-service/internal/httputil"
	"user-service/internal/middleware"
	"user-service/internal/service"

	"github.com/gin-gonic/gin"
)

type UserService interface {
	ListUsers(ctx context.Context, tenantID string) ([]domain.User, error)
	UpdateUser(ctx context.Context, input service.UpdateUserInput) error
}

type UserHandler struct {
	userService UserService
}

func NewUserHandler(userService UserService) *UserHandler {
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
	Name string `json:"name" binding:"required,max=255"`
}

// UpdateMeResponse is the transport DTO returned after a profile mutation.
type UpdateMeResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

func (userHandler *UserHandler) ListUsers(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		httputil.WriteError(c, http.StatusUnauthorized, "user handler: missing tenant_id in token claims")
		return
	}

	users, err := userHandler.userService.ListUsers(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "failed to list users: "+err.Error())
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
		httputil.WriteValidationError(c, err)
		return
	}

	input := service.UpdateUserInput{
		UserID: userID,
		Name:   req.Name,
	}

	if err := userHandler.userService.UpdateUser(c.Request.Context(), input); err != nil {
		if errors.Is(err, domain.ErrUserIDRequired) ||
			errors.Is(err, domain.ErrUserNameRequired) {
			httputil.WriteError(c, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, domain.ErrNotFound) {
			httputil.WriteError(c, http.StatusNotFound, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, "failed to update user profile: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "User profile updated successfully", UpdateMeResponse{
		UserID: userID,
		Name:   req.Name,
	})
}
