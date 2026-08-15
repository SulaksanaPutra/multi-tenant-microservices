package consumer

import (
	"context"
	"testing"

	"notification-service/internal/infrastructure/rabbitmq"
)

type mockAMQPInterfaceClient struct{}

func (m *mockAMQPInterfaceClient) ConnContext() context.Context {
	return context.Background()
}

func (m *mockAMQPInterfaceClient) WaitUntilReady(ctx context.Context) error {
	return nil
}

func (m *mockAMQPInterfaceClient) DeclareExchange(name, kind string) error {
	return nil
}

func (m *mockAMQPInterfaceClient) DeclareAndBindQueue(queueName, exchangeName, routingKey string) error {
	return nil
}

func (m *mockAMQPInterfaceClient) Consume(queueName, consumerTag string) (<-chan rabbitmq.Delivery, error) {
	ch := make(chan rabbitmq.Delivery)
	close(ch)
	return ch, nil
}

func TestAMQPClient_InterfaceSatisfaction(t *testing.T) {
	var client AMQPClient = &mockAMQPInterfaceClient{}
	if client.ConnContext() == nil {
		t.Fatal("expected ConnContext to return non-nil context")
	}
}
