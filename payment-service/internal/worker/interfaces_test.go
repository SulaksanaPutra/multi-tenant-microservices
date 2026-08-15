package worker

// interfaces_test.go verifies that this package's worker test mocks satisfy
// every interface declared in interfaces.go (compile-time only).

var _ OutboxRepository = (*mockWorkerOutboxRepo)(nil)
var _ EventPublisher = (*mockWorkerPublisher)(nil)
var _ PaymentSweeper = (*testSweeper)(nil)
