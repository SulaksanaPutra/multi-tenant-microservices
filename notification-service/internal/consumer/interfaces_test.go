package consumer

import (
	"context"

	"notification-service/internal/infrastructure/rabbitmq"
)

// mockAMQPClient provides the AMQP interface implementation for all consumer tests.
// The remaining mocks (mockTxManager, mockNotificationService, mockInboxService,
// mockAuthClient, mockMailer) are declared in user_created_consumer_test.go.
type mockAMQPClient struct{}

func (m *mockAMQPClient) ConnContext() context.Context { return context.Background() }
func (m *mockAMQPClient) WaitUntilReady(ctx context.Context) error { return nil }
func (m *mockAMQPClient) DeclareExchange(name, kind string) error { return nil }
func (m *mockAMQPClient) DeclareAndBindQueue(queueName, exchangeName, routingKey string) error {
	return nil
}
func (m *mockAMQPClient) Consume(queueName, consumerTag string) (<-chan rabbitmq.Delivery, error) {
	ch := make(chan rabbitmq.Delivery)
	close(ch)
	return ch, nil
}

// ---------------------------------------------------------------------------
// Compile-time interface satisfaction checks for ALL interfaces in interfaces.go
// ---------------------------------------------------------------------------

var _ AMQPClient = (*mockAMQPClient)(nil)
var _ TxManager = (*mockTxManager)(nil)
var _ InboxService = (*mockInboxService)(nil)
var _ NotificationService = (*mockNotificationService)(nil)
var _ OrderNotificationService = (*mockNotificationService)(nil)
var _ AuthClient = (*mockAuthClient)(nil)
var _ Mailer = (*mockMailer)(nil)
