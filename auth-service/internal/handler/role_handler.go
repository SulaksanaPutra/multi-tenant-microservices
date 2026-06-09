package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"auth-service/internal/domain"
	"auth-service/internal/httputil"
	"auth-service/internal/middleware"
	"auth-service/internal/service"

	"github.com/gin-gonic/gin"
)

type RoleService interface {
	CreateRole(ctx context.Context, input service.CreateRoleInput) (*domain.Role, error)
	GetRole(ctx context.Context, roleID string) (*domain.Role, error)
	ListRolesForTenant(ctx context.Context, tenantID string) ([]domain.Role, error)
	UpdateRolePermissions(ctx context.Context, input service.UpdateRolePermissionsInput) error
	DeleteRole(ctx context.Context, roleID string) error
	AssignUserRole(ctx context.Context, input service.AssignUserRoleInput) error
	GetUserRole(ctx context.Context, userID, tenantID string) (*domain.UserRole, error)
	ListUserRolesForTenant(ctx context.Context, tenantID string, userIDs []string) ([]service.UserRoleAssignmentOutput, error)
}

type CreateRoleRequest struct {
	TenantID      string   `json:"tenant_id"`
	Name          string   `json:"name"           binding:"required,max=255"`
	Description   string   `json:"description"`
	Permissions   []string `json:"permissions"`
	PermissionIDs []string `json:"permission_ids"`
}

type UpdateRolePermissionsRequest struct {
	PermissionIDs []string `json:"permission_ids"`
	Permissions   []string `json:"permissions"`
}

type AssignUserRoleRequest struct {
	TenantID string `json:"tenant_id"`
	RoleID   string `json:"role_id"   binding:"required"`
}

// RoleResponse is the transport DTO for a role returned to API consumers.
type RoleResponse struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	IsSystem    bool                 `json:"is_system"`
	CreatedAt   time.Time            `json:"created_at"`
	Permissions []PermissionResponse `json:"permissions"`
}

// UserRoleResponse is the transport DTO for a user's role assignment.
type UserRoleResponse struct {
	UserID     string        `json:"user_id"`
	TenantID   string        `json:"tenant_id"`
	RoleID     string        `json:"role_id"`
	AssignedAt time.Time     `json:"assigned_at"`
	AssignedBy *string       `json:"assigned_by,omitempty"`
	Role       *RoleResponse `json:"role,omitempty"`
}

// UserRoleAssignmentResponse is the transport DTO for a lightweight "user + role" row.
type UserRoleAssignmentResponse struct {
	UserID   string `json:"user_id"`
	RoleID   string `json:"role_id"`
	RoleName string `json:"role_name"`
}

type RoleHandler struct {
	roleService RoleService
}

func NewRoleHandler(roleService RoleService) *RoleHandler {
	return &RoleHandler{roleService: roleService}
}

func toRoleResponse(role domain.Role) RoleResponse {
	permissions := make([]PermissionResponse, len(role.Permissions))
	for i, permission := range role.Permissions {
		permissions[i] = toPermissionResponse(permission)
	}
	return RoleResponse{
		ID:          role.ID,
		Name:        role.Name,
		Description: role.Description,
		IsSystem:    role.IsSystem,
		CreatedAt:   role.CreatedAt,
		Permissions: permissions,
	}
}

func toUserRoleResponse(ur domain.UserRole) UserRoleResponse {
	var role *RoleResponse
	if ur.Role != nil {
		roleResponse := toRoleResponse(*ur.Role)
		role = &roleResponse
	}
	return UserRoleResponse{
		UserID:     ur.UserID,
		TenantID:   ur.TenantID,
		RoleID:     ur.RoleID,
		AssignedAt: ur.AssignedAt,
		AssignedBy: ur.AssignedBy,
		Role:       role,
	}
}

func toUserRoleAssignmentResponse(assignment service.UserRoleAssignmentOutput) UserRoleAssignmentResponse {
	return UserRoleAssignmentResponse{
		UserID:   assignment.UserID,
		RoleID:   assignment.RoleID,
		RoleName: assignment.RoleName,
	}
}

func (h *RoleHandler) CreateRole(c *gin.Context) {
	var req CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		tenantID = req.TenantID
	}
	if tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "tenant_id parameter or JWT token claim is required")
		return
	}

	role, err := h.roleService.CreateRole(c.Request.Context(), service.CreateRoleInput{
		TenantID:    tenantID,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		if errors.Is(err, domain.ErrRoleAlreadyExists) {
			httputil.WriteError(c, http.StatusConflict, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	perms := req.PermissionIDs
	if len(perms) == 0 {
		perms = req.Permissions
	}
	if len(perms) > 0 {
		if err := h.roleService.UpdateRolePermissions(c.Request.Context(), service.UpdateRolePermissionsInput{
			RoleID:        role.ID,
			PermissionIDs: perms,
		}); err != nil {
			httputil.WriteError(c, http.StatusInternalServerError, err.Error())
			return
		}
		if reloadedRole, err := h.roleService.GetRole(c.Request.Context(), role.ID); err == nil {
			role = reloadedRole
		}
	}

	httputil.WriteSuccess(c, http.StatusCreated, "Role created successfully", toRoleResponse(*role))
}

func (h *RoleHandler) GetRole(c *gin.Context) {
	roleID := c.Param("id")
	if roleID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "role id parameter is required")
		return
	}

	role, err := h.roleService.GetRole(c.Request.Context(), roleID)
	if err != nil {
		if errors.Is(err, domain.ErrRoleNotFound) {
			httputil.WriteError(c, http.StatusNotFound, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Role retrieved successfully", toRoleResponse(*role))
}

func (h *RoleHandler) ListRoles(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		tenantID = c.Query("tenant_id")
	}
	if tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "tenant_id query parameter or token claim is required")
		return
	}

	roles, err := h.roleService.ListRolesForTenant(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	roleResponses := make([]RoleResponse, len(roles))
	for i, role := range roles {
		roleResponses[i] = toRoleResponse(role)
	}

	httputil.WriteSuccess(c, http.StatusOK, "Roles retrieved successfully", roleResponses)
}

func (h *RoleHandler) UpdateRolePermissions(c *gin.Context) {
	roleID := c.Param("id")
	if roleID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "role id parameter is required")
		return
	}

	var req UpdateRolePermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	permIDs := req.PermissionIDs
	if len(permIDs) == 0 {
		permIDs = req.Permissions
	}

	if err := h.roleService.UpdateRolePermissions(c.Request.Context(), service.UpdateRolePermissionsInput{
		RoleID:        roleID,
		PermissionIDs: permIDs,
	}); err != nil {
		if errors.Is(err, domain.ErrSystemRoleProtected) {
			httputil.WriteError(c, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, domain.ErrRoleNotFound) {
			httputil.WriteError(c, http.StatusNotFound, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "Role permissions updated successfully", nil)
}

func (h *RoleHandler) DeleteRole(c *gin.Context) {
	roleID := c.Param("id")
	if roleID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "role id parameter is required")
		return
	}

	if err := h.roleService.DeleteRole(c.Request.Context(), roleID); err != nil {
		if errors.Is(err, domain.ErrSystemRoleProtected) {
			httputil.WriteError(c, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, domain.ErrRoleNotFound) {
			httputil.WriteError(c, http.StatusNotFound, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "Role deleted successfully", nil)
}

func (h *RoleHandler) AssignUserRole(c *gin.Context) {
	userID := c.Param("userID")
	if userID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "user id parameter is required")
		return
	}

	var req AssignUserRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		tenantID = req.TenantID
	}
	if tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "tenant_id parameter or JWT token claim is required")
		return
	}

	if err := h.roleService.AssignUserRole(c.Request.Context(), service.AssignUserRoleInput{
		UserID:   userID,
		TenantID: tenantID,
		RoleID:   req.RoleID,
	}); err != nil {
		if errors.Is(err, domain.ErrRoleNotFound) {
			httputil.WriteError(c, http.StatusNotFound, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess[any](c, http.StatusOK, "User role assigned successfully", nil)
}

func (h *RoleHandler) GetUserRole(c *gin.Context) {
	userID := c.Param("userID")
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		tenantID = c.Query("tenant_id")
	}
	if userID == "" || tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "userID path param and tenant_id are required")
		return
	}

	ur, err := h.roleService.GetUserRole(c.Request.Context(), userID, tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrRoleNotFound) {
			httputil.WriteError(c, http.StatusNotFound, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "User role retrieved successfully", toUserRoleResponse(*ur))
}

// ListUserRoles returns role assignments for a set of user IDs within the
// caller's tenant. Supports a comma-separated (?user_ids=a,b,c) or repeated
// (?user_ids=a&user_ids=b) query parameter so the frontend can compose a
// "users + roles" table in a single round-trip.
func (h *RoleHandler) ListUserRoles(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "tenant_id JWT token claim is required")
		return
	}

	raws := c.QueryArray("user_ids")
	var userIDs []string
	for _, raw := range raws {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				userIDs = append(userIDs, part)
			}
		}
	}
	if len(userIDs) == 0 {
		httputil.WriteError(c, http.StatusBadRequest, "at least one user_ids query parameter is required")
		return
	}

	assignments, err := h.roleService.ListUserRolesForTenant(c.Request.Context(), tenantID, userIDs)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	assignmentResponses := make([]UserRoleAssignmentResponse, len(assignments))
	for i, assignment := range assignments {
		assignmentResponses[i] = toUserRoleAssignmentResponse(assignment)
	}

	httputil.WriteSuccess(c, http.StatusOK, "User roles retrieved successfully", assignmentResponses)
}
