package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Client struct {
	Conn     *amqp.Connection
	Channel  *amqp.Channel
	amqpURL  string
	mu       sync.RWMutex
	isClosed bool

	ctx     context.Context
	cancel  context.CancelFunc
	readyCh chan struct{}
}

func NewClient(amqpURL string) (*Client, error) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		amqpURL: amqpURL,
		ctx:     ctx,
		cancel:  cancel,
		readyCh: make(chan struct{}),
	}

	if err := c.connect(); err != nil {
		cancel()
		return nil, err
	}

	close(c.readyCh)
	go c.watchConnection()
	return c, nil
}

func (c *Client) connect() error {
	var conn *amqp.Connection
	var err error

	for i := 0; i < 10; i++ {
		conn, err = amqp.Dial(c.amqpURL)
		if err == nil {
			log.Println("Tenant Service RabbitMQ Driver: Connected successfully")
			break
		}
		log.Printf("Tenant Service RabbitMQ connection attempt %d/10 failed: %v. Retrying in 2s...", i+1, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ driver: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to open RabbitMQ channel: %w", err)
	}

	if err := ch.Confirm(false); err != nil {
		log.Printf("Tenant Service RabbitMQ Driver Warning: Failed to enable Publisher Confirms: %v", err)
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

		c.mu.Lock()
		c.cancel()
		c.readyCh = make(chan struct{})
		c.mu.Unlock()

		log.Printf("Tenant Service RabbitMQ Driver: Connection dropped (reason: %v). Reconnecting...", closeErr)

		for {
			c.mu.RLock()
			if c.isClosed {
				c.mu.RUnlock()
				return
			}
			c.mu.RUnlock()

			if err := c.connect(); err == nil {
				c.mu.Lock()
				c.ctx, c.cancel = context.WithCancel(context.Background())
				close(c.readyCh)
				c.mu.Unlock()

				log.Println("Tenant Service RabbitMQ Driver: Reconnection established successfully!")
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
}

func (c *Client) ConnContext() context.Context {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ctx
}

func (c *Client) WaitUntilReady(ctx context.Context) error {
	c.mu.RLock()
	ready := c.readyCh
	c.mu.RUnlock()

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) NotifyReconnect() <-chan struct{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.readyCh
}

func (c *Client) DeclareExchange(name, kind string) error {
	c.mu.RLock()
	ch := c.Channel
	c.mu.RUnlock()

	if ch == nil {
		return fmt.Errorf("channel is nil")
	}

	err := ch.ExchangeDeclare(
		name,
		kind,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to declare exchange %s: %w", name, err)
	}
	return nil
}

func (c *Client) DeclareAndBindQueue(queueName, exchangeName, routingKey string) error {
	c.mu.RLock()
	ch := c.Channel
	c.mu.RUnlock()

	if ch == nil {
		return fmt.Errorf("channel is nil")
	}

	q, err := ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("failed to declare queue %s: %w", queueName, err)
	}

	err = ch.QueueBind(q.Name, routingKey, exchangeName, false, nil)
	if err != nil {
		return fmt.Errorf("failed to bind queue %s to exchange %s: %w", queueName, exchangeName, err)
	}
	return nil
}

func (c *Client) PublishEvent(ctx context.Context, exchangeName, routingKey string, payload interface{}) error {
	return c.PublishEventWithConfirm(ctx, exchangeName, routingKey, payload)
}

func (c *Client) PublishEventWithConfirm(ctx context.Context, exchangeName, routingKey string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal event payload: %w", err)
	}

	c.mu.RLock()
	ch := c.Channel
	c.mu.RUnlock()

	if ch == nil {
		return fmt.Errorf("channel is nil")
	}

	deferred, err := ch.PublishWithDeferredConfirmWithContext(
		ctx,
		exchangeName,
		routingKey,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish event: %w", err)
	}

	if deferred != nil {
		confirmCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		ok, err := deferred.WaitContext(confirmCtx)
		if err != nil || !ok {
			return fmt.Errorf("publisher confirm failed (ack=%v): %w", ok, err)
		}
	}

	return nil
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.isClosed = true
	if c.cancel != nil {
		c.cancel()
	}
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
