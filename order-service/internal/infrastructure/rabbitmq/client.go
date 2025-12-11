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

// Client wraps an AMQP connection with automatic reconnection, a connection-lifetime
// context for instant disconnect detection, and a closed-channel broadcast for
// zero-CPU reconnect waiting.
type Client struct {
	mu      sync.RWMutex
	amqpURL string
	Conn    *amqp.Connection
	Channel *amqp.Channel

	isClosed bool

	// ctx is cancelled the instant the TCP socket drops — before any retry sleep.
	// Consumers select on <-client.ConnContext().Done() to detect disconnect immediately.
	ctx    context.Context
	cancel context.CancelFunc

	// readyCh is closed when the connection is ready.
	// It is reset to a new unclosed channel on each disconnect, then closed again
	// after a successful reconnect. Consumers call WaitUntilReady(ctx) to park
	// with zero CPU until the connection is re-established.
	readyCh chan struct{}
}

func NewClient(amqpURL string) (*Client, error) {
	// Initialize ctx, cancel, and readyCh at construction time.
	// This guarantees ConnContext() and WaitUntilReady() are always safe to call —
	// even if invoked by consumers before the first watchConnection() cycle completes.
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

	// Signal that the initial connection is ready.
	// Consumers calling WaitUntilReady() before Start() returns will unblock instantly.
	close(c.readyCh)

	go c.watchConnection()

	return c, nil
}

// connect dials RabbitMQ and opens a channel. It retries up to 10 times with a
// 2-second sleep between attempts. It is called from NewClient() and watchConnection().
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

	if err := ch.Confirm(false); err != nil {
		log.Printf("Order Service RabbitMQ Driver Warning: Failed to enable Publisher Confirms: %v", err)
	}

	c.mu.Lock()
	c.Conn = conn
	c.Channel = ch
	c.mu.Unlock()

	return nil
}

// watchConnection monitors the active connection and drives the reconnect lifecycle.
// On socket drop it immediately cancels the connection context (notifying all consumers
// before any sleep), resets readyCh, then reconnects and broadcasts recovery.
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

		// Block until the broker closes the connection.
		closeErr := <-conn.NotifyClose(make(chan *amqp.Error, 1))

		c.mu.RLock()
		closed := c.isClosed
		c.mu.RUnlock()
		if closed {
			return
		}

		// 1. Immediately cancel the connection context.
		//    Any goroutine selecting on <-c.ConnContext().Done() receives the signal
		//    right now — before the first time.Sleep(2s) in the reconnect loop below.
		//    cancel() is idempotent and non-blocking.
		c.mu.Lock()
		c.cancel()
		// 2. Reset readyCh so that consumers calling WaitUntilReady() will block
		//    until the new connection is ready.
		c.readyCh = make(chan struct{})
		c.mu.Unlock()

		log.Printf("Order Service RabbitMQ Driver: Connection dropped (reason: %v). Attempting reconnection...", closeErr)

		// 3. Reconnect loop with fixed 2-second backoff at the driver level.
		for {
			c.mu.RLock()
			if c.isClosed {
				c.mu.RUnlock()
				return
			}
			c.mu.RUnlock()

			if err := c.connect(); err == nil {
				c.mu.Lock()
				// 4. Fresh context for the new connection lifetime.
				c.ctx, c.cancel = context.WithCancel(context.Background())
				// 5. Broadcast reconnection to all WaitUntilReady() callers.
				//    Closing a channel wakes every goroutine blocked on <-readyCh simultaneously.
				close(c.readyCh)
				c.mu.Unlock()

				log.Println("Order Service RabbitMQ Driver: Reconnection established successfully!")
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
}

// ConnContext returns the context bound to the current active AMQP connection lifetime.
// It is cancelled the instant the TCP socket drops, before any reconnect retry sleep.
// Always returns a non-nil context (initialized in NewClient).
func (c *Client) ConnContext() context.Context {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ctx
}

// WaitUntilReady blocks until the RabbitMQ connection is established or ctx is cancelled.
// Uses a closed-channel broadcast — zero CPU footprint while waiting.
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

// NotifyReconnect returns a channel that is closed when the connection is established or recovered.
func (c *Client) NotifyReconnect() <-chan struct{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.readyCh
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
	return c.PublishEventWithConfirm(ctx, exchangeName, routingKey, payload)
}

func (c *Client) PublishEventWithConfirm(ctx context.Context, exchangeName, routingKey string, payload interface{}) error {
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

