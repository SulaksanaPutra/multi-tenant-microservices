package handler

import (
	"encoding/json"
	"net/http"

	"user-service/internal/service"
)

type RegisterRequest struct {
	Email      string `json:"email"`
	Name       string `json:"name"`
	TenantName string `json:"tenant_name"`
	TenantSlug string `json:"tenant_slug"`
}

type RegisterResponse struct {
	Status   string `json:"status"`
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`
}

type UserHandler struct {
	userService service.UserService
}

func NewUserHandler(userService service.UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

func (h *UserHandler) RegisterUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	if req.Email == "" || req.Name == "" || req.TenantName == "" || req.TenantSlug == "" {
		WriteError(w, http.StatusBadRequest, "email, name, tenant_name, and tenant_slug are required")
		return
	}

	// Delegate to UserService business layer
	input := service.RegisterUserInput{
		Email:      req.Email,
		Name:       req.Name,
		TenantName: req.TenantName,
		TenantSlug: req.TenantSlug,
	}

	output, err := h.userService.RegisterUser(r.Context(), input)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Return HTTP 202 Accepted with RegisterResponse DTO
	WriteJSON(w, http.StatusAccepted, RegisterResponse{
		Status:   "accepted",
		UserID:   output.UserID,
		TenantID: output.TenantID,
	})
}
