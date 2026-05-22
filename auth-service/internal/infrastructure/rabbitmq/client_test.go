package rabbitmq

import (
	"context"
	"testing"
	"time"
)

func TestClient_Close_Nil(t *testing.T) {
	// Nil receiver must not panic.
	var nilClient *Client
	nilClient.Close()

	// Non-nil struct with nil Conn and Channel must not panic either.
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

func TestClient_ConnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{ctx: ctx}
	if client.ConnContext() == nil {
		t.Fatal("expected non-nil ConnContext")
	}
}

func TestClient_WaitUntilReady_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readyCh := make(chan struct{})
	client := &Client{readyCh: readyCh}

	cancel()

	err := client.WaitUntilReady(ctx)
	if err == nil {
		t.Fatal("expected error from WaitUntilReady when context is cancelled")
	}
}

func TestClient_WaitUntilReady_AlreadyReady(t *testing.T) {
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
		t.Fatal("timeout waiting for WaitUntilReady")
	}
}

func TestClient_NotifyReconnect_ReturnsReadyChannel(t *testing.T) {
	readyCh := make(chan struct{})
	client := &Client{readyCh: readyCh}

	if got := client.NotifyReconnect(); got == nil {
		t.Fatal("expected non-nil channel from NotifyReconnect")
	}
}

func TestClient_DeclareExchange_NilChannel(t *testing.T) {
	client := &Client{}
	if err := client.DeclareExchange("test.exchange", "topic"); err == nil {
		t.Fatal("expected error declaring exchange with nil channel, got nil")
	}
}

func TestClient_DeclareAndBindQueue_NilChannel(t *testing.T) {
	client := &Client{}
	if err := client.DeclareAndBindQueue("test.queue", "test.exchange", "test.key", nil); err == nil {
		t.Fatal("expected error declaring queue with nil channel, got nil")
	}
}

func TestClient_PublishEventWithConfirm_NilChannel(t *testing.T) {
	client := &Client{}
	err := client.PublishEventWithConfirm(context.Background(), "test.exchange", "test.key", map[string]string{"foo": "bar"})
	if err == nil {
		t.Fatal("expected error publishing with nil channel, got nil")
	}
}
