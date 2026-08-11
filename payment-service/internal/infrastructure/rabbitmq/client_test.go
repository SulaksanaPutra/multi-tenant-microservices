package rabbitmq

import (
	"context"
	"testing"
	"time"
)

func TestClient_NilChannelGuards(t *testing.T) {
	c := &Client{}
	if err := c.DeclareExchange("company.events", "topic"); err == nil {
		t.Error("expected error when declaring exchange on nil channel")
	}
	if err := c.PublishEvent(context.Background(), "routing.key", []byte("test")); err == nil {
		t.Error("expected error when publishing event on nil channel")
	}
	if err := c.PublishStruct(context.Background(), "company.events", "routing.key", map[string]string{"foo": "bar"}); err == nil {
		t.Error("expected error when publishing struct on nil channel")
	}
}

func TestClient_CloseNilSafe(t *testing.T) {
	var c *Client
	c.Close() // Should not panic

	readyCh := make(chan struct{})
	close(readyCh)
	c = &Client{
		readyCh: readyCh,
		cancel:  func() {},
	}
	c.Close()
	if !c.isClosed {
		t.Error("expected client to be marked closed")
	}
}

func TestClient_WaitUntilReady(t *testing.T) {
	readyCh := make(chan struct{})
	close(readyCh)
	c := &Client{readyCh: readyCh}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := c.WaitUntilReady(ctx); err != nil {
		t.Errorf("expected ready channel to succeed immediately, got %v", err)
	}

	blockCh := make(chan struct{})
	cBlocked := &Client{readyCh: blockCh}
	cancelCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn()

	if err := cBlocked.WaitUntilReady(cancelCtx); err == nil {
		t.Error("expected error when context is canceled while waiting for ready")
	}
}
