package consumer

import (
	"context"

	"user-service/internal/infrastructure/rabbitmq"
)

// mockAMQPInterfaceClient provides the AMQP interface implementation for consumer tests.
// Remaining mocks (mockTxManager, mockUserService, mockInboxService) are declared in
// workspace_initiated_consumer_test.go.
type mockAMQPInterfaceClient struct{}

func (m *mockAMQPInterfaceClient) ConnContext() context.Context { return context.Background() }
func (m *mockAMQPInterfaceClient) WaitUntilReady(ctx context.Context) error { return nil }
func (m *mockAMQPInterfaceClient) DeclareExchange(name, kind string) error { return nil }
func (m *mockAMQPInterfaceClient) DeclareAndBindQueue(queueName, exchangeName, routingKey string) error {
	return nil
}
func (m *mockAMQPInterfaceClient) Consume(queueName, consumerTag string) (<-chan rabbitmq.Delivery, error) {
	ch := make(chan rabbitmq.Delivery)
	close(ch)
	return ch, nil
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks for ALL interfaces in interfaces.go
// ---------------------------------------------------------------------------

var _ AMQPClient = (*mockAMQPInterfaceClient)(nil)
var _ TxManager = (*mockTxManager)(nil)
var _ InboxService = (*mockInboxService)(nil)
var _ UserService = (*mockUserService)(nil)
