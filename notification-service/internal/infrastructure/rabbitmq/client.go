package rabbitmq

import (
	"context"
	"encoding/json"
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
			log.Println("Notification Service RabbitMQ Driver: Connected successfully")
			break
		}
		log.Printf("Notification Service RabbitMQ connection attempt %d/10 failed: %v. Retrying in 2s...", i+1, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ driver: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to open RabbitMQ channel: %w", err)
	}

	return &Client{
		Conn:    conn,
		Channel: ch,
	}, nil
}

func (c *Client) DeclareExchange(name, kind string) error {
	err := c.Channel.ExchangeDeclare(
		name,  // exchange name
		kind,  // type e.g. "topic"
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,   // arguments
	)
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

	err = c.Channel.QueueBind(q.Name, routingKey, exchangeName, false, nil)
	if err != nil {
		return fmt.Errorf("failed to bind queue %s to exchange %s: %w", queueName, exchangeName, err)
	}
	return nil
}

func (c *Client) PublishEvent(ctx context.Context, exchangeName, routingKey string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal event payload: %w", err)
	}

	if c == nil || c.Channel == nil {
		return fmt.Errorf("channel is nil")
	}

	return c.Channel.PublishWithContext(
		ctx,
		exchangeName,
		routingKey,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
}

func (c *Client) Close() {
	if c != nil {
		if c.Channel != nil {
			_ = c.Channel.Close()
		}
		if c.Conn != nil {
			_ = c.Conn.Close()
		}
	}
}
