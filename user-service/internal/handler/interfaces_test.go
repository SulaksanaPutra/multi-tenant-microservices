package handler

// interfaces_test.go verifies that this package's handler test mocks satisfy
// every interface declared in interfaces.go (compile-time only).

var _ UserService = (*mockUserService)(nil)
