package rabbitmq

import (
	"context"
	"testing"
	"time"
)

func TestClient_Close_Nil(t *testing.T) {
	// Test nil struct pointer
	var nilClient *Client
	nilClient.Close()

	// Test non-nil struct with nil Conn and Channel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{
		amqpURL: "amqp://guest:guest@localhost:5672/",
		ctx:     ctx,
		cancel:  cancel,
		readyCh: make(chan struct{}),
	}
	client.Close()
}

func TestConnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{ctx: ctx}
	if client.ConnContext() == nil {
		t.Fatalf("expected non-nil ConnContext")
	}
}

func TestWaitUntilReady_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readyCh := make(chan struct{})

	client := &Client{readyCh: readyCh}

	cancel()

	err := client.WaitUntilReady(ctx)
	if err == nil {
		t.Fatalf("expected error from WaitUntilReady when context is cancelled")
	}
}

func TestWaitUntilReady_AlreadyReady(t *testing.T) {
	ctx := context.Background()
	readyCh := make(chan struct{})
	close(readyCh)

	client := &Client{readyCh: readyCh}

	done := make(chan error)
	go func() {
		done <- client.WaitUntilReady(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error from WaitUntilReady: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for WaitUntilReady")
	}
}

func TestPublishEvent_NilChannel(t *testing.T) {
	client := &Client{Conn: nil, Channel: nil}
	err := client.PublishEvent(context.Background(), "test.exchange", "test.key", map[string]string{"foo": "bar"})
	if err == nil {
		t.Fatalf("expected error publishing with nil channel, got nil")
	}
}

func TestDeclareExchange_NilChannel(t *testing.T) {
	client := &Client{}
	err := client.DeclareExchange("test.exchange", "topic")
	if err == nil {
		t.Fatalf("expected error declaring exchange with nil channel, got nil")
	}
}

func TestDeclareAndBindQueue_NilChannel(t *testing.T) {
	client := &Client{}
	err := client.DeclareAndBindQueue("test.queue", "test.exchange", "test.key")
	if err == nil {
		t.Fatalf("expected error declaring queue with nil channel, got nil")
	}
}

