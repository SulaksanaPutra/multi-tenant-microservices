package rabbitmq

import (
	"context"
	"testing"
)

func TestClient_Close_Nil(t *testing.T) {
	// Test nil struct pointer
	var nilClient *Client
	nilClient.Close()

	// Test non-nil struct with nil Conn and Channel
	client := &Client{Conn: nil, Channel: nil}
	client.Close()
}

func TestPublishEvent_NilChannel(t *testing.T) {
	client := &Client{Conn: nil, Channel: nil}
	err := client.PublishEvent(context.Background(), "test.exchange", "test.key", map[string]string{"foo": "bar"})
	if err == nil {
		t.Fatalf("expected error publishing with nil channel, got nil")
	}
}
