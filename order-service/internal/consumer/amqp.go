package consumer

import (
	"context"

	"order-service/internal/infrastructure/rabbitmq"
)

// AMQPClient is the consumer-side interface defining the AMQP transport contract.
type AMQPClient interface {
	ConnContext() context.Context
	WaitUntilReady(ctx context.Context) error
	DeclareExchange(name, kind string) error
	DeclareAndBindQueue(queueName, exchangeName, routingKey string) error
	DeclareAndBindExclusiveQueue(exchangeName, routingKey string) (string, error)
	Consume(queueName, consumerTag string) (<-chan rabbitmq.Delivery, error)
}
