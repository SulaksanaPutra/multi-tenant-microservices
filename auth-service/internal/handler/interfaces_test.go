package handler

// interfaces_test.go verifies that this package's handler test mocks satisfy
// every interface declared in interfaces.go (compile-time only).

var _ AuthService = (*mockAuthService)(nil)
var _ RoleService = (*mockRoleService)(nil)
var _ PermissionService = (*mockPermissionService)(nil)
var _ InternalAuthAppService = (*mockInternalAuthService)(nil)
var _ InternalPermissionService = (*mockInternalPermissionService)(nil)
