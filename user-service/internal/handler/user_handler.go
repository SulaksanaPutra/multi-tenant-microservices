package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
	"user-service/internal/domain"
	"user-service/internal/service"

	"github.com/gin-gonic/gin"
)

type UserHandler struct {
	userService UserService
}

func NewUserHandler(userService UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

type ListUsersResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UpdateUserRequest struct {
	Name string `json:"name" binding:"required,min=3,max=50"`
}

func toUserResponse(u service.UserOutput) ListUsersResponse {
	return ListUsersResponse{
		ID:        u.ID,
		Email:     u.Email,
		Name:      u.Name,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
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
		userResponses = append(userResponses, toUserResponse(u))
	}
	httputil.WriteSuccess(c, http.StatusOK, "Users retrieved successfully", userResponses)
}

func (userHandler *UserHandler) GetMe(c *gin.Context) {
	userID := c.GetString(middleware.ContextKeyUserID)
	if userID == "" {
		httputil.WriteError(c, http.StatusUnauthorized, "user handler: missing user_id in token claims")
		return
	}

	user, err := userHandler.userService.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			httputil.WriteError(c, http.StatusNotFound, "user handler: user not found")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, "user handler: failed to fetch user profile: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "User Profile Fetched", toUserResponse(*user))
}

func (userHandler *UserHandler) UpdateMe(c *gin.Context) {
	userID := c.GetString(middleware.ContextKeyUserID)
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

	user, err := userHandler.userService.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, "failed to reload user profile after update: "+err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "User profile updated successfully", toUserResponse(*user))
}
