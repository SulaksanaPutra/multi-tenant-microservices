package worker

// interfaces_test.go verifies that this package's worker test mocks satisfy
// every interface declared in interfaces.go (compile-time only).

var _ OutboxRepository = (*mockOutboxRepository)(nil)
var _ TenantDBResolver = (*mockTenantDBResolver)(nil)
var _ TenantLister = (*mockTenantLister)(nil)
var _ RoutingStatusChecker = (*mockRoutingStatusChecker)(nil)
var _ OrderEventPublisher = (*mockOrderEventPublisher)(nil)
