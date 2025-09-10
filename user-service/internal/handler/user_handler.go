package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"user-service/internal/utils"

	"user-service/internal/service"
	"user-service/internal/txctx"
)

type RegisterRequest struct {
	Email      string `json:"email"`
	Name       string `json:"name"`
	TenantName string `json:"tenant_name"`
	TenantSlug string `json:"tenant_slug"`
}

type RegisterResponseData struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`
}

type UserHandler struct {
	db          *sql.DB
	userService service.UserService
}

type UserHandlerParams struct {
	DB          *sql.DB
	UserService service.UserService
}

func NewUserHandler(params UserHandlerParams) *UserHandler {
	return &UserHandler{
		db:          params.DB,
		userService: params.UserService,
	}
}

func (h *UserHandler) RegisterUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		utils.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	if req.Email == "" || req.Name == "" || req.TenantName == "" || req.TenantSlug == "" {
		utils.WriteError(w, http.StatusBadRequest, "email, name, tenant_name, and tenant_slug are required")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to start database transaction: %v", err))
		return
	}
	defer func(tx *sql.Tx) {
		err := tx.Rollback()
		if err != nil {
			utils.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to rollback transaction: %v", err))
		}
	}(tx)

	ctx := txctx.WithTx(r.Context(), tx)
	input := service.RegisterUserInput{
		Email:      req.Email,
		Name:       req.Name,
		TenantName: req.TenantName,
		TenantSlug: req.TenantSlug,
	}

	output, err := h.userService.RegisterUser(ctx, input)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("failed to commit transaction: %v", err))
		return
	}

	utils.WriteSuccess(w, http.StatusAccepted, "User registration accepted", RegisterResponseData{
		UserID:   output.UserID,
		TenantID: output.TenantID,
	})
}
