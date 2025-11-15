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
	mu          sync.RWMutex
	amqpURL     string
	Conn        *amqp.Connection
	Channel     *amqp.Channel
	reconnectCh chan struct{}
	isClosed    bool
}

func NewClient(amqpURL string) (*Client, error) {
	client := &Client{
		amqpURL:     amqpURL,
		reconnectCh: make(chan struct{}, 1),
	}

	if err := client.connect(); err != nil {
		return nil, err
	}

	go client.watchConnection()

	return client, nil
}

func (c *Client) connect() error {
	var conn *amqp.Connection
	var err error

	for i := 0; i < 10; i++ {
		conn, err = amqp.Dial(c.amqpURL)
		if err == nil {
			log.Println("Order Service RabbitMQ Driver: Connected successfully")
			break
		}
		log.Printf("Order Service RabbitMQ connection attempt %d/10 failed: %v. Retrying in 2s...", i+1, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to open RabbitMQ channel: %w", err)
	}

	c.mu.Lock()
	c.Conn = conn
	c.Channel = ch
	c.mu.Unlock()

	return nil
}

func (c *Client) watchConnection() {
	for {
		c.mu.RLock()
		if c.isClosed {
			c.mu.RUnlock()
			return
		}
		conn := c.Conn
		c.mu.RUnlock()

		if conn == nil {
			time.Sleep(1 * time.Second)
			continue
		}

		closeErr := <-conn.NotifyClose(make(chan *amqp.Error, 1))
		c.mu.RLock()
		closed := c.isClosed
		c.mu.RUnlock()

		if closed {
			return
		}

		log.Printf("Order Service RabbitMQ Driver: Connection dropped (reason: %v). Attempting reconnection...", closeErr)

		// Reconnection loop with backoff
		for {
			c.mu.RLock()
			if c.isClosed {
				c.mu.RUnlock()
				return
			}
			c.mu.RUnlock()

			if err := c.connect(); err == nil {
				log.Println("Order Service RabbitMQ Driver: Reconnection established successfully!")
				// Broadcast notification to active consumers
				select {
				case c.reconnectCh <- struct{}{}:
				default:
				}
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
}

// NotifyReconnect returns a channel that receives a signal when RabbitMQ reconnects.
func (c *Client) NotifyReconnect() <-chan struct{} {
	return c.reconnectCh
}

func (c *Client) DeclareExchange(name, kind string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.Channel == nil {
		return fmt.Errorf("channel is nil")
	}
	err := c.Channel.ExchangeDeclare(name, kind, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("failed to declare exchange %s: %w", name, err)
	}
	return nil
}

func (c *Client) DeclareAndBindQueue(queueName, exchangeName, routingKey string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.Channel == nil {
		return fmt.Errorf("channel is nil")
	}
	q, err := c.Channel.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("failed to declare queue %s: %w", queueName, err)
	}
	if err := c.Channel.QueueBind(q.Name, routingKey, exchangeName, false, nil); err != nil {
		return fmt.Errorf("failed to bind queue %s: %w", queueName, err)
	}
	return nil
}

func (c *Client) PublishEvent(ctx context.Context, exchangeName, routingKey string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	c.mu.RLock()
	ch := c.Channel
	c.mu.RUnlock()

	if ch == nil {
		return fmt.Errorf("channel is nil")
	}

	return ch.PublishWithContext(
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
	c.mu.Lock()
	c.isClosed = true
	ch := c.Channel
	conn := c.Conn
	c.mu.Unlock()

	if ch != nil {
		_ = ch.Close()
	}
	if conn != nil {
		_ = conn.Close()
	}
}
