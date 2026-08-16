package worker

// interfaces_test.go verifies that this package's worker test mocks satisfy
// every interface declared in interfaces.go (compile-time only).

var _ OutboxRepository = (*mockOutboxRepository)(nil)
var _ UserEventPublisher = (*mockUserEventPublisher)(nil)
