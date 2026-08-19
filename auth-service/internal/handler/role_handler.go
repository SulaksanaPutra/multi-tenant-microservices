package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"auth-service/internal/domain"
	"auth-service/internal/service"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"

	"github.com/gin-gonic/gin"
)

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

type RoleResponse struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	IsSystem    bool                 `json:"is_system"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	Permissions []PermissionResponse `json:"permissions"`
}

type UserRoleResponse struct {
	UserID     string        `json:"user_id"`
	TenantID   string        `json:"tenant_id"`
	RoleID     string        `json:"role_id"`
	AssignedAt time.Time     `json:"assigned_at"`
	AssignedBy *string       `json:"assigned_by,omitempty"`
	Role       *RoleResponse `json:"role,omitempty"`
}

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

func toRoleResponse(role service.RoleOutput) RoleResponse {
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
		UpdatedAt:   role.UpdatedAt,
		Permissions: permissions,
	}
}

func toUserRoleResponse(userRoleOutput service.UserRoleOutput) UserRoleResponse {
	var role *RoleResponse
	if userRoleOutput.Role != nil {
		roleResponse := toRoleResponse(*userRoleOutput.Role)
		role = &roleResponse
	}
	return UserRoleResponse{
		UserID:     userRoleOutput.UserID,
		TenantID:   userRoleOutput.TenantID,
		RoleID:     userRoleOutput.RoleID,
		AssignedAt: userRoleOutput.AssignedAt,
		AssignedBy: userRoleOutput.AssignedBy,
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

func (roleHandler *RoleHandler) CreateRole(c *gin.Context) {
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

	role, err := roleHandler.roleService.CreateRole(c.Request.Context(), service.CreateRoleInput{
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
		if err := roleHandler.roleService.UpdateRolePermissions(c.Request.Context(), service.UpdateRolePermissionsInput{
			RoleID:        role.ID,
			PermissionIDs: perms,
		}); err != nil {
			httputil.WriteError(c, http.StatusInternalServerError, err.Error())
			return
		}
		if reloadedRole, err := roleHandler.roleService.GetRoleByID(c.Request.Context(), role.ID); err == nil {
			role = reloadedRole
		}
	}

	httputil.WriteSuccess(c, http.StatusCreated, "Role created successfully", toRoleResponse(*role))
}

func (roleHandler *RoleHandler) GetRoleByID(c *gin.Context) {
	roleID := c.Param("id")
	if roleID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "role id parameter is required")
		return
	}

	role, err := roleHandler.roleService.GetRoleByID(c.Request.Context(), roleID)
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

func (roleHandler *RoleHandler) ListRoles(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		tenantID = c.Query("tenant_id")
	}
	if tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "tenant_id query parameter or token claim is required")
		return
	}

	roles, err := roleHandler.roleService.ListRolesForTenant(c.Request.Context(), tenantID)
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

func (roleHandler *RoleHandler) UpdateRolePermissions(c *gin.Context) {
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

	if err := roleHandler.roleService.UpdateRolePermissions(c.Request.Context(), service.UpdateRolePermissionsInput{
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

	updatedRole, err := roleHandler.roleService.GetRoleByID(c.Request.Context(), roleID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "Role permissions updated successfully", toRoleResponse(*updatedRole))
}

func (roleHandler *RoleHandler) DeleteRole(c *gin.Context) {
	roleID := c.Param("id")
	if roleID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "role id parameter is required")
		return
	}

	if err := roleHandler.roleService.DeleteRole(c.Request.Context(), roleID); err != nil {
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

func (roleHandler *RoleHandler) AssignUserRole(c *gin.Context) {
	userID := c.Param("user_id")
	if userID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "user_id path parameter is required")
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

	if err := roleHandler.roleService.AssignUserRole(c.Request.Context(), service.AssignUserRoleInput{
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

func (roleHandler *RoleHandler) GetUserRole(c *gin.Context) {
	userID := c.Param("user_id")
	tenantID := c.GetString(middleware.ContextKeyTenantID)
	if tenantID == "" {
		tenantID = c.Query("tenant_id")
	}
	if userID == "" || tenantID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "user_id path param and tenant_id are required")
		return
	}

	userRoleOutput, err := roleHandler.roleService.GetUserRole(c.Request.Context(), userID, tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrRoleNotFound) {
			httputil.WriteError(c, http.StatusNotFound, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "User role retrieved successfully", toUserRoleResponse(*userRoleOutput))
}

func (roleHandler *RoleHandler) ListUserRoles(c *gin.Context) {
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

	assignments, err := roleHandler.roleService.ListUserRolesForTenant(c.Request.Context(), tenantID, userIDs)
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
