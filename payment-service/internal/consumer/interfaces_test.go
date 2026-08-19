package consumer

var _ AMQPClient = (*mockAMQPClient)(nil)
var _ TxManager = (*mockTxManager)(nil)
var _ InboxService = (*mockInboxService)(nil)
var _ DebtService = (*mockDebtService)(nil)

