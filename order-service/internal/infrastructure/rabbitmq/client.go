package rabbitmq

import (
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Client struct {
	Conn    *amqp.Connection
	Channel *amqp.Channel
}

func NewClient(amqpURL string) (*Client, error) {
	var conn *amqp.Connection
	var err error

	for i := 0; i < 10; i++ {
		conn, err = amqp.Dial(amqpURL)
		if err == nil {
			log.Println("Order Service RabbitMQ Driver: Connected successfully")
			break
		}
		log.Printf("Order Service RabbitMQ connection attempt %d/10 failed: %v. Retrying in 2s...", i+1, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		err := conn.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("failed to open RabbitMQ channel: %w", err)
	}

	return &Client{Conn: conn, Channel: ch}, nil
}

func (c *Client) DeclareExchange(name, kind string) error {
	err := c.Channel.ExchangeDeclare(name, kind, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("failed to declare exchange %s: %w", name, err)
	}
	return nil
}

func (c *Client) DeclareAndBindQueue(queueName, exchangeName, routingKey string) error {
	q, err := c.Channel.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("failed to declare queue %s: %w", queueName, err)
	}
	if err := c.Channel.QueueBind(q.Name, routingKey, exchangeName, false, nil); err != nil {
		return fmt.Errorf("failed to bind queue %s: %w", queueName, err)
	}
	return nil
}

func (c *Client) Close() {
	if c.Channel != nil {
		err := c.Channel.Close()
		if err != nil {
			return
		}
	}
	if c.Conn != nil {
		err := c.Conn.Close()
		if err != nil {
			return
		}
	}
}
