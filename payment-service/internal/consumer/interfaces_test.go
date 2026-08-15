package consumer

// interfaces_test.go verifies that this package's consumer test mocks satisfy
// every interface declared in interfaces.go (compile-time only).
//
// All mock implementations live in order_created_consumer_test.go:
//   - mockAMQPClient     → AMQPClient
//   - mockTxManager      → TxManager
//   - mockInboxService   → InboxService
//   - mockInitiator      → PaymentInitiator

var _ AMQPClient = (*mockAMQPClient)(nil)
var _ TxManager = (*mockTxManager)(nil)
var _ InboxService = (*mockInboxService)(nil)
var _ PaymentInitiator = (*mockInitiator)(nil)
