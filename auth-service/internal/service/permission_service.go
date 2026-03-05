package service

// RegisterPermissionsInput is an alias for InternalRegisterPermissionsInput for backward compatibility.
type RegisterPermissionsInput = InternalRegisterPermissionsInput

// NewPermissionService returns InternalPermissionService for backward compatibility.
func NewPermissionService(permRepo PermissionRepository, roleRepo RoleRepository) *InternalPermissionService {
	return NewInternalPermissionService(permRepo, roleRepo)
}
