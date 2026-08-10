package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Delivery = amqp.Delivery

type Client struct {
	mu       sync.RWMutex
	amqpURL  string
	Conn     *amqp.Connection
	Channel  *amqp.Channel
	isClosed bool
	ctx      context.Context
	cancel   context.CancelFunc
	readyCh  chan struct{}
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
		return nil, fmt.Errorf("rabbitmq: initial connection failed: %w", err)
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
			log.Println("Payment Service RabbitMQ Driver: Connected successfully")
			break
		}
		log.Printf("Payment Service RabbitMQ connection attempt %d/10 failed: %v. Retrying in 2s...", i+1, err)
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

	if err := ch.Confirm(false); err != nil {
		log.Printf("Payment Service RabbitMQ Driver Warning: Failed to enable Publisher Confirms: %v", err)
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
		ch := c.Channel
		c.mu.RUnlock()

		if conn == nil || ch == nil {
			time.Sleep(1 * time.Second)
			continue
		}

		var closeErr *amqp.Error
		select {
		case closeErr = <-conn.NotifyClose(make(chan *amqp.Error, 1)):
		case closeErr = <-ch.NotifyClose(make(chan *amqp.Error, 1)):
		}

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

		log.Printf("Payment Service RabbitMQ Driver: Connection/channel closed (reason: %v). Reconnecting...", closeErr)

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
				log.Println("Payment Service RabbitMQ Driver: Reconnected successfully!")
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

func (c *Client) DeclareExchange(name, kind string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.Channel == nil {
		return errors.New("channel is nil")
	}
	return c.Channel.ExchangeDeclare(name, kind, true, false, false, false, nil)
}

func (c *Client) PublishEvent(ctx context.Context, routingKey string, payload []byte) error {
	c.mu.RLock()
	ch := c.Channel
	c.mu.RUnlock()

	if ch == nil {
		return errors.New("channel is nil")
	}

	deferred, err := ch.PublishWithDeferredConfirmWithContext(
		ctx,
		"company.events",
		routingKey,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         payload,
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

func (c *Client) PublishStruct(ctx context.Context, exchangeName, routingKey string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	c.mu.RLock()
	ch := c.Channel
	c.mu.RUnlock()

	if ch == nil {
		return errors.New("channel is nil")
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
		return fmt.Errorf("failed to publish struct: %w", err)
	}

	if deferred != nil {
		confirmCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		ok, err := deferred.WaitContext(confirmCtx)
		if err != nil || !ok {
			return fmt.Errorf("publisher confirm failed: %w", err)
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
	c.cancel()
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
